package messaging

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"workspace/src/internal/clients/gcsclient"
	messagingdb "workspace/src/internal/db/messaging"
	"workspace/src/internal/utils"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const maxAttachmentBytes = 200 << 20 // 200MB

// signedURLExpiry is how long a generated attachment URL stays valid before
// the frontend needs to re-fetch the message list to get a fresh one.
const signedURLExpiry = 1 * time.Hour

var allowedAttachmentTypes = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"image/gif":       "gif",
	"image/webp":      "webp",
	"video/mp4":       "mp4",
	"video/webm":      "webm",
	"video/quicktime": "mov",
}

// UploadMessageAttachmentHandler handles POST /messaging/conversations/{id}/attachments
// Accepts multipart/form-data with field "file". Returns {"url","kind","name","size_bytes"}.
func UploadMessageAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}

	bucket := os.Getenv("GCS_BUCKET_NAME")
	if bucket == "" || gcsclient.GetClient() == nil {
		utils.WriteError(w, http.StatusServiceUnavailable, "Attachments are not configured")
		return
	}

	if err := r.ParseMultipartForm(maxAttachmentBytes); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Failed to parse multipart form: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Missing file field: "+err.Error())
		return
	}
	defer file.Close()

	if header.Size > maxAttachmentBytes {
		utils.WriteError(w, http.StatusBadRequest, "File too large (max 200MB)")
		return
	}

	contentType := header.Header.Get("Content-Type")
	ext, allowed := allowedAttachmentTypes[contentType]
	if !allowed {
		fallbackExt := strings.ToLower(filepath.Ext(header.Filename))
		if fallbackExt != "" {
			fallbackExt = fallbackExt[1:]
		}
		validExts := map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "mp4": true, "webm": true, "mov": true, "m4v": true}
		if !validExts[fallbackExt] {
			utils.WriteError(w, http.StatusBadRequest, "Unsupported attachment type: "+contentType)
			return
		}
		ext = fallbackExt
		if ext == "jpeg" {
			ext = "jpg"
			contentType = "image/jpeg"
		}
	}

	kind := "file"
	if strings.HasPrefix(contentType, "video/") {
		kind = "video"
	} else if strings.HasPrefix(contentType, "image/") {
		kind = "image"
	}

	key := fmt.Sprintf("workspace-messaging/%s/%s.%s", conversationID, uuid.New().String(), ext)
	if err := gcsclient.Upload(r.Context(), bucket, key, file, contentType); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Upload failed: "+err.Error())
		return
	}

	url, err := gcsclient.SignedURL(bucket, key, signedURLExpiry)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to sign attachment URL: "+err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"key":        key,
		"url":        url,
		"kind":       kind,
		"name":       header.Filename,
		"size_bytes": header.Size,
	})
}
