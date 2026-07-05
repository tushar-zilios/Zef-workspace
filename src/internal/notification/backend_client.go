package notification

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"time"
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
			return
		}
		resp.Body.Close()
	}()
}
