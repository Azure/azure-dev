package azdext

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var logFile *os.File

// setupDailyLogger configures the logger to write to a daily timestamped log file.
func SetupDailyLogger() error {
	// Determine the directory of the executing binary.
	ex, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	execDir := filepath.Dir(ex)

	// Create the logs folder relative to the executable.
	logDir := filepath.Join(execDir, "logs")
	if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create a filename like "YYYY-MM-DD.log" in the logs folder.
	dateStr := time.Now().Format("2006-01-02")
	logFilePath := filepath.Join(logDir, fmt.Sprintf("%s.log", dateStr))

	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	logFile = f

	// Set logger output and enable standard timestamp flags.
	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	setupSignalHandler()

	log.Println("Logger initialized.")

	return nil
}

// setupSignalHandler listens for SIGINT to flush the log.
func setupSignalHandler() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT)
	go func() {
		sig := <-c
		log.Printf("Received %v, flushing log and exiting.", sig)
		if logFile != nil {
			logFile.Sync()
		}
		os.Exit(0)
	}()
}
