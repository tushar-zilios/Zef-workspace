package gcsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

var (
	client          *storage.Client
	credsEmail      string
	credsPrivateKey []byte
)

type serviceAccountKey struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// Init creates a GCS client. GOOGLE_APPLICATION_CREDENTIALS may hold either a
// file path to a service-account JSON key (standard ADC behavior) or the raw
// JSON key content itself (convenient for env-var-only deployments). The
// service account's email/private key are cached for signing object URLs,
// since the bucket is kept private and objects are only ever served via
// short-lived signed URLs (never made public).
func Init(ctx context.Context) error {
	raw := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
	var jsonBytes []byte
	var opts []option.ClientOption

	if strings.HasPrefix(raw, "{") {
		jsonBytes = []byte(raw)
		opts = append(opts, option.WithCredentialsJSON(jsonBytes))
	} else if raw != "" {
		fileBytes, err := os.ReadFile(raw)
		if err != nil {
			return fmt.Errorf("gcsclient.Init: failed to read credentials file: %w", err)
		}
		jsonBytes = fileBytes
	}

	if len(jsonBytes) > 0 {
		var sa serviceAccountKey
		if err := json.Unmarshal(jsonBytes, &sa); err != nil {
			return fmt.Errorf("gcsclient.Init: failed to parse credentials JSON: %w", err)
		}
		credsEmail = sa.ClientEmail
		credsPrivateKey = []byte(sa.PrivateKey)
	}

	c, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("gcsclient.Init: %w", err)
	}
	client = c
	return nil
}

// GetClient returns the initialized GCS client.
func GetClient() *storage.Client {
	return client
}

// Upload writes r to bucket/key. The bucket is expected to be private —
// callers must use SignedURL to hand out temporary access.
func Upload(ctx context.Context, bucket, key string, r io.Reader, contentType string) error {
	if client == nil {
		return fmt.Errorf("gcsclient not initialized")
	}
	obj := client.Bucket(bucket).Object(key)
	w := obj.NewWriter(ctx)
	w.ContentType = contentType

	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return fmt.Errorf("gcsclient.Upload copy: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("gcsclient.Upload close: %w", err)
	}
	return nil
}

// SignedURL returns a temporary signed GET URL for bucket/key, valid for expiry.
func SignedURL(bucket, key string, expiry time.Duration) (string, error) {
	if client == nil || credsEmail == "" {
		return "", fmt.Errorf("gcsclient not initialized with signing credentials")
	}
	return client.Bucket(bucket).SignedURL(key, &storage.SignedURLOptions{
		GoogleAccessID: credsEmail,
		PrivateKey:     credsPrivateKey,
		Method:         "GET",
		Expires:        time.Now().Add(expiry),
	})
}

// Delete removes bucket/key from GCS.
func Delete(ctx context.Context, bucket, key string) error {
	if client == nil {
		return fmt.Errorf("gcsclient not initialized")
	}
	return client.Bucket(bucket).Object(key).Delete(ctx)
}

// Close closes the GCS client.
func Close() {
	if client != nil {
		_ = client.Close()
	}
}
