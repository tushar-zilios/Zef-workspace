package worker

import (
	"context"
	"time"

	messagingdb "workspace/src/internal/db/messaging"
	messagingHandlers "workspace/src/internal/handlers/messaging"
	"workspace/src/internal/logger"
)

// StartCronWorker ticks every minute and dispatches scheduled messages whose
// send time has come due. Mirrors Zef-backend's worker/cron.go reminder dispatch shape.
func StartCronWorker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	ticker := time.NewTicker(1 * time.Minute)

	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				dispatchDueScheduledMessages(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	return done
}

func dispatchDueScheduledMessages(ctx context.Context) {
	due, err := messagingdb.ListDueScheduledMessages(ctx)
	if err != nil {
		logger.LogDB("Failed to list due scheduled messages: %v", err)
		return
	}
	for i := range due {
		msg := &due[i]
		if err := messagingdb.MarkMessageSent(ctx, msg.MessageID); err != nil {
			logger.LogDB("Failed to mark scheduled message %s sent: %v", msg.MessageID, err)
			continue
		}
		msg.Status = "sent"
		messagingHandlers.BroadcastMessageEvent(ctx, "message", msg)
	}
}
