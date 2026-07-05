package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

var (
	HandlersLogFile *os.File
	DBLogFile       *os.File
	HandlersLogger  *log.Logger
	DBLogger        *log.Logger
	logsDir         = "logs"
)

func Init() error {
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	hFile, err := os.OpenFile(filepath.Join(logsDir, "handlers.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create handlers.log: %w", err)
	}
	HandlersLogFile = hFile
	HandlersLogger = log.New(HandlersLogFile, "[HANDLERS] ", log.LstdFlags)

	dFile, err := os.OpenFile(filepath.Join(logsDir, "db.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create db.log: %w", err)
	}
	DBLogFile = dFile
	DBLogger = log.New(DBLogFile, "[DB] ", log.LstdFlags)

	return nil
}

func Cleanup() {
	if HandlersLogFile != nil {
		HandlersLogFile.Close()
	}
	if DBLogFile != nil {
		DBLogFile.Close()
	}
}

func LogDB(format string, args ...any) {
	if DBLogger != nil {
		DBLogger.Printf(format, args...)
	} else {
		log.Printf("[DB] "+format, args...)
	}
}

func LogHandler(format string, args ...any) {
	if HandlersLogger != nil {
		HandlersLogger.Printf(format, args...)
	} else {
		log.Printf("[HANDLER] "+format, args...)
	}
}
