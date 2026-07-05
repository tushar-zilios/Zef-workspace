package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL           string
	JWTSecret             string
	JWTExpirationHours    int
	Port                  string
	GCSBucketName         string
	BackendURL            string
	InternalServiceSecret string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	expHours := 24
	if v := os.Getenv("JWT_EXPIRATION_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			expHours = n
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}

	backendURL := os.Getenv("BACKEND_URL")
	if backendURL == "" {
		backendURL = "http://localhost:8080"
	}

	return &Config{
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		JWTSecret:             os.Getenv("JWT_SECRET"),
		JWTExpirationHours:    expHours,
		Port:                  port,
		GCSBucketName:         os.Getenv("GCS_BUCKET_NAME"),
		BackendURL:            backendURL,
		InternalServiceSecret: os.Getenv("INTERNAL_SERVICE_SECRET"),
	}, nil
}
