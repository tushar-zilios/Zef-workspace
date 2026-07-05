package db

import (
	"context"
	"math/rand"
	"time"
)

func retryWithExponentialBackoff(ctx context.Context, maxAttempts int, initialBackoff, maxBackoff time.Duration, op func() error, logFunc func(string, ...any)) error {
	var err error
	backoff := initialBackoff

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		err = op()
		if err == nil {
			return nil
		}
		if attempt == maxAttempts {
			break
		}
		var jitter time.Duration
		if backoff > 5 {
			jitter = time.Duration(rand.Int63n(int64(backoff / 5)))
		}
		sleepTime := backoff + jitter
		if sleepTime > maxBackoff {
			sleepTime = maxBackoff
		}
		logFunc("Attempt %d failed: %v. Retrying in %v...", attempt, err, sleepTime)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepTime):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return err
}
