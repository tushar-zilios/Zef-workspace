package messaging

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestUploadMessageAttachmentHandler_NotMember exercises the membership check, which runs
// before any GCS configuration is consulted, so it doesn't require GCS_BUCKET_NAME to be set.
func TestUploadMessageAttachmentHandler_NotMember(t *testing.T) {
	requirePool(t)
	_, _, _, conversationID := seedConversation(t)
	outsider := uniqueKey("outsider-")

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/attachments", UploadMessageAttachmentHandler)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.Close()

	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/attachments", &buf), outsider)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUploadMessageAttachmentHandler_MemberButGCSUnconfigured verifies that once membership
// passes, an unconfigured GCS bucket (the expected state for this service, whose DB/infra is
// "not yet provisioned" per root CLAUDE.md) yields 503 rather than a panic.
func TestUploadMessageAttachmentHandler_MemberButGCSUnconfigured(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/attachments", UploadMessageAttachmentHandler)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.Close()

	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/attachments", &buf), userA)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Only assert this when GCS truly isn't configured; if it happens to be configured in the
	// test environment, the multipart form (with no "file" field) still yields a 400.
	if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 503 (GCS unconfigured) or 400 (missing file), got %d: %s", w.Code, w.Body.String())
	}
}
