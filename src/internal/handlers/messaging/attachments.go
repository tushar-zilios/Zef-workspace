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
	"application/pdf": "pdf",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
	"text/plain":      "txt",
	"text/csv":        "csv",
	"application/zip": "zip",
}

// docxFamilyExts disambiguates the modern Office formats from a plain .zip: all three
// (docx/xlsx/pptx) are themselves zip containers, so http.DetectContentType can only ever
// sniff them down to the generic "application/zip" signature. Once the sniff confirms the
// upload IS a zip container, it's safe to trust the client-declared Content-Type to pick
// the specific member type — spoofing it only ever downgrades/upgrades between these
// document kinds, never smuggles in an unrelated file type.
var docxFamilyExts = map[string]string{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
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
	contentType := sniffedType

	// docx/xlsx/pptx are themselves zip containers, so the sniff can only ever confirm
	// "application/zip" for them — once that's confirmed, trust the client-declared
	// Content-Type to pick the specific Office kind (see docxFamilyExts doc comment).
	if sniffedType == "application/zip" {
		if declared := header.Header.Get("Content-Type"); declared != "" {
			if _, ok := docxFamilyExts[declared]; ok {
				contentType = declared
			}
		}
	}
	// Plain-text sniffs collapse csv into "text/plain"; trust the declared type to tell
	// them apart once the sniff has confirmed the content really is plain text.
	if strings.HasPrefix(sniffedType, "text/plain") {
		if header.Header.Get("Content-Type") == "text/csv" {
			contentType = "text/csv"
		} else {
			contentType = "text/plain"
		}
	}

	ext, allowed := allowedAttachmentTypes[contentType]
	if !allowed {
		utils.WriteError(w, http.StatusBadRequest, "Unsupported or unrecognized attachment content")
		return
	}

	kind := "document"
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
