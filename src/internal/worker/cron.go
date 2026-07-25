package worker

import (
	"context"
	"os"
	"time"

	"workspace/src/internal/clients/gcsclient"
	messagingdb "workspace/src/internal/db/messaging"
	messagingHandlers "workspace/src/internal/handlers/messaging"
	"workspace/src/internal/logger"
)

// messageRetentionPeriod is how long a soft-deleted message (and its GCS attachment, if any)
// is kept before being purged for good. Soft-delete alone leaves rows and storage objects
// growing unbounded forever.
const messageRetentionPeriod = 30 * 24 * time.Hour

// StartCronWorker ticks every minute and dispatches scheduled messages whose
// send time has come due. Mirrors Zef-backend's worker/cron.go reminder dispatch shape.
func StartCronWorker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	ticker := time.NewTicker(1 * time.Minute)
	purgeTicker := time.NewTicker(1 * time.Hour)

	go func() {
		defer close(done)
		defer ticker.Stop()
		defer purgeTicker.Stop()
		for {
			select {
			case <-ticker.C:
				dispatchDueScheduledMessages(ctx)
			case <-purgeTicker.C:
				purgeExpiredMessages(ctx)
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

// purgeExpiredMessages hard-deletes soft-deleted messages (and their GCS attachment object,
// if any) once they've aged past messageRetentionPeriod. Runs in bounded batches so a large
// backlog doesn't hold the DB connection or block the next tick.
func purgeExpiredMessages(ctx context.Context) {
	bucket := os.Getenv("GCS_BUCKET_NAME")
	cutoff := time.Now().Add(-messageRetentionPeriod)
	const batchSize = 200

	for {
		batch, err := messagingdb.ListPurgeableMessages(ctx, cutoff, batchSize)
		if err != nil {
			logger.LogDB("Failed to list purgeable messages: %v", err)
			return
		}
		if len(batch) == 0 {
			return
		}
		for i := range batch {
			msg := &batch[i]
			if msg.AttachmentKey != nil && bucket != "" && gcsclient.GetClient() != nil {
				if err := gcsclient.Delete(ctx, bucket, *msg.AttachmentKey); err != nil {
					logger.LogDB("Failed to delete attachment %s for purged message %s: %v", *msg.AttachmentKey, msg.MessageID, err)
					continue
				}
			}
			if err := messagingdb.HardDeleteMessage(ctx, msg.MessageID); err != nil {
				logger.LogDB("Failed to hard-delete message %s: %v", msg.MessageID, err)
			}
		}
		if len(batch) < batchSize {
			return
		}
	}
}
