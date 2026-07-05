package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"workspace/src/internal/clients/gcsclient"
	"workspace/src/internal/config"
	"workspace/src/internal/db"
	"workspace/src/internal/logger"
	"workspace/src/internal/routes"
	"workspace/src/internal/worker"
)

func main() {
	if err := run(); err != nil {
		log.Printf("Fatal error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	if err := logger.Init(); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer logger.Cleanup()

	fmt.Println("Starting Zef Workspace service...")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	port := cfg.Port

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, err = db.InitDB(dbCtx, cfg.DatabaseURL)
	dbCancel()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	if db.PoolReady() {
		log.Println("DB connection pool initialized successfully.")
	} else {
		log.Println("DB not configured (DATABASE_URL not set) — endpoints will return errors.")
	}
	defer db.CloseDB()

	if cfg.GCSBucketName != "" {
		gcsCtx, gcsCancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := gcsclient.Init(gcsCtx); err != nil {
			log.Printf("GCS not initialized (attachments disabled): %v", err)
		} else {
			log.Println("GCS client initialized successfully.")
		}
		gcsCancel()
	} else {
		log.Println("GCS_BUCKET_NAME not set — message attachments disabled.")
	}
	defer gcsclient.Close()

	cronCtx, cronCancel := context.WithCancel(context.Background())
	defer cronCancel()
	worker.StartCronWorker(cronCtx)

	router := routes.NewRouter()
	serverAddr := ":" + port

	log.Printf("Workspace service starting on %s", serverAddr)
	if err := http.ListenAndServe(serverAddr, router); err != nil {
		return fmt.Errorf("HTTP server failed: %w", err)
	}

	return nil
}
