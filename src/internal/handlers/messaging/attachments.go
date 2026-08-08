package messaging

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"workspace/src/internal/clients/audit"
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
	"audio/webm":      "weba",
	"audio/ogg":       "ogg",
	"audio/mpeg":      "mp3",
	"audio/wav":       "wav",
	"audio/x-wav":     "wav",
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

	// Sniff the actual file content (magic bytes) rather than trusting the client-supplied
	// Content-Type header or filename extension, either of which can be freely spoofed to
	// smuggle disallowed file types past the allowlist.
	sniffBuf := make([]byte, 512)
	n, err := io.ReadFull(file, sniffBuf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		utils.WriteError(w, http.StatusBadRequest, "Failed to read file: "+err.Error())
		return
	}
	sniffBuf = sniffBuf[:n]
	sniffedType := http.DetectContentType(sniffBuf)
	// QuickTime/MP4 containers sniff to a generic "video/mp4" or octet-stream depending on
	// atom layout; narrow to the declared type only when the sniff agrees on the broad kind.
	ext, allowed := allowedAttachmentTypes[sniffedType]
	if !allowed {
		utils.WriteError(w, http.StatusBadRequest, "Unsupported or unrecognized attachment content")
		return
	}
	contentType := sniffedType

	kind := "file"
	if strings.HasPrefix(contentType, "video/") {
		kind = "video"
	} else if strings.HasPrefix(contentType, "image/") {
		kind = "image"
	} else if strings.HasPrefix(contentType, "audio/") {
		kind = "audio"
	}

	uploadReader := io.MultiReader(bytes.NewReader(sniffBuf), file)
	key := fmt.Sprintf("workspace-messaging/%s/%s.%s", conversationID, uuid.New().String(), ext)
	if err := gcsclient.Upload(r.Context(), bucket, key, uploadReader, contentType); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Upload failed: "+err.Error())
		return
	}

	url, err := gcsclient.SignedURL(bucket, key, signedURLExpiry)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to sign attachment URL: "+err.Error())
		return
	}

	audit.Publish("message.attachment.upload", "conversation", conversationID, uid, map[string]any{
		"kind":       kind,
		"size_bytes": header.Size,
	})
	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"key":        key,
		"url":        url,
		"kind":       kind,
		"name":       header.Filename,
		"size_bytes": header.Size,
	})
}
