// Package audit publishes cross-service audit events to the shared Kafka
// topic "audit-events", consumed by Zef-audit. No-op if Kafka wasn't configured.
package audit

import (
	"context"
	"encoding/json"
	"time"

	"workspace/src/internal/db"
	"workspace/src/internal/logger"
)

type Event struct {
	Service      string          `json:"service"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type,omitempty"`
	ResourceID   string          `json:"resource_id,omitempty"`
	PerformedBy  string          `json:"performed_by,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

// Publish fires an audit event asynchronously so callers never block or fail
// their own request on audit-log delivery.
func Publish(action, resourceType, resourceID, performedBy string, metadata any) {
	go func() {
		meta, err := json.Marshal(metadata)
		if err != nil {
			logger.LogHandler("[AUDIT] failed to marshal metadata: %v", err)
			meta = nil
		}

		payload, err := json.Marshal(Event{
			Service:      "workspace",
			Action:       action,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			PerformedBy:  performedBy,
			Metadata:     meta,
		})
		if err != nil {
			logger.LogHandler("[AUDIT] failed to marshal event: %v", err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.Publish(ctx, "audit-events", []byte(resourceID), payload); err != nil {
			logger.LogHandler("[AUDIT] failed to publish event (%s %s): %v", action, resourceID, err)
		}
	}()
}
