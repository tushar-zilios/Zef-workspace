package notification

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"workspace/src/internal/logger"
)

var backendHTTPClient = &http.Client{Timeout: 5 * time.Second}

type backendNotificationRequest struct {
	UserID       string `json:"user_id"`
	Module       string `json:"module"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	Message      string `json:"message"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	SenderID     string `json:"sender_id"`
}

// NotifyBackend feeds Zef-backend's global notification module (POST /internal/notifications)
// so workspace events (e.g. new messages) show up in the same cross-service notification feed
// as task/reminder notifications. Best-effort: errors are swallowed since this is a side channel
// alongside the workspace's own SSE broadcast, not the primary delivery path.
func NotifyBackend(userID, module, notifType, title, message, resourceType, resourceID, senderID string) {
	backendURL := os.Getenv("BACKEND_URL")
	secret := os.Getenv("INTERNAL_SERVICE_SECRET")
	if backendURL == "" || secret == "" {
		return
	}

	body, err := json.Marshal(backendNotificationRequest{
		UserID:       userID,
		Module:       module,
		Type:         notifType,
		Title:        title,
		Message:      message,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		SenderID:     senderID,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, backendURL+"/internal/notifications/", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", secret)

	go func() {
		resp, err := backendHTTPClient.Do(req)
		if err != nil {
			logger.LogHandler("NotifyBackend: delivery to backend failed for user %s: %v", userID, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			logger.LogHandler("NotifyBackend: backend returned %d for user %s: %s", resp.StatusCode, userID, string(body))
		}
	}()
}

type backendMessagingEventRequest struct {
	UserID  string          `json:"user_id"`
	Payload json.RawMessage `json:"payload"`
}

// NotifyBackendMessagingEvent forwards a live messaging event (new message, edit, delete,
// reaction, view) to Zef-backend's shared SSE stream (POST /internal/messaging/events) for
// delivery to userID, replacing this service's own /messaging/stream EventSource — browsers
// cap concurrent HTTP/1.1 connections per origin at 6, and a second long-lived stream to the
// same origin (Zef-backend, via the frontend's /workspace-svc proxy) ate one permanently.
// Best-effort: errors are swallowed since there is no other consumer of this event.
func NotifyBackendMessagingEvent(userID string, payload json.RawMessage) {
	backendURL := os.Getenv("BACKEND_URL")
	secret := os.Getenv("INTERNAL_SERVICE_SECRET")
	if backendURL == "" || secret == "" {
		return
	}

	body, err := json.Marshal(backendMessagingEventRequest{UserID: userID, Payload: payload})
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, backendURL+"/internal/messaging/events", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", secret)

	go func() {
		resp, err := backendHTTPClient.Do(req)
		if err != nil {
			logger.LogHandler("NotifyBackendMessagingEvent: relay to backend failed for user %s: %v", userID, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			logger.LogHandler("NotifyBackendMessagingEvent: backend returned %d for user %s: %s", resp.StatusCode, userID, string(body))
		}
	}()
}
