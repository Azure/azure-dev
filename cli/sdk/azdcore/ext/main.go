package ext

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	azcorelog "github.com/Azure/azure-sdk-for-go/sdk/azcore/log"

	"github.com/spf13/pflag"
)

var logFile *os.File

func init() {
	if isDebugEnabled() {
		azcorelog.SetListener(func(event azcorelog.Event, msg string) {
			log.Printf("%s: %s\n", event, msg)
		})
	} else {
		var err error

		exePath, err := os.Executable()
		if err != nil {
			log.Fatalf("Failed to get executable name: %v", err)
		}

		exeNameWithExt := filepath.Base(exePath)                                    // Get the base name of the executable
		exeName := strings.TrimSuffix(exeNameWithExt, filepath.Ext(exeNameWithExt)) // Remove the extension

		currentDate := time.Now().Format("20060102")        // Format the current date as YYYYMMDD
		logFileName := exeName + "-" + currentDate + ".log" // Log file name based on the date

		logFile, err = os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			log.Fatalf("Failed to open log file: %v", err)
		}

		// Set log output to the file
		log.SetOutput(logFile)

		// Optional: Adds timestamp and file information to each log entry
		log.SetFlags(log.LstdFlags | log.Lshortfile)

		// Register the signal handler to ensure log file is closed gracefully
		setupSignalHandler()
	}
}

// setupSignalHandler listens for system signals (e.g., SIGINT, SIGTERM) and ensures cleanup
func setupSignalHandler() {
	signals := make(chan os.Signal, 1)
	// Notify channel on SIGINT (Ctrl+C) or SIGTERM (termination)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// Goroutine to handle the signals
	go func() {
		// Block until we receive a signal
		sig := <-signals
		log.Printf("Received signal: %v, shutting down gracefully...", sig)

		// Perform cleanup
		flush()
	}()
}

// flush ensures the log file is closed
func flush() {
	if logFile != nil {
		logFile.Sync()
	}
}

// isDebugEnabled checks to see if `--debug` was passed with a truthy
// value.
func isDebugEnabled() bool {
	debug := false
	flags := pflag.NewFlagSet("", pflag.ContinueOnError)

	// Since we are running this parse logic on the full command line, there may be additional flags
	// which we have not defined in our flag set (but would be defined by whatever command we end up
	// running). Setting UnknownFlags instructs `flags.Parse` to continue parsing the command line
	// even if a flag is not in the flag set (instead of just returning an error saying the flag was not
	// found).
	flags.ParseErrorsWhitelist.UnknownFlags = true
	flags.BoolVar(&debug, "debug", false, "")

	// if flag `-h` of `--help` is within the command, the usage is automatically shown.
	// Setting `Usage` to a no-op will hide this extra unwanted output.
	flags.Usage = func() {}

	_ = flags.Parse(os.Args[1:])
	return debug
}
