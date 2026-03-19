package log

import (
	"fmt"
	"log"
	"os"
)

var inActions = os.Getenv("GITHUB_ACTIONS") == "true"

// Notice logs an informational message.
func Notice(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if inActions {
		log.Printf("::notice::%s", msg)
	} else {
		log.Println(msg)
	}
}

// Warn logs a warning.
func Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if inActions {
		log.Printf("::warning::%s", msg)
	} else {
		log.Printf("WARNING: %s", msg)
	}
}

// Error logs an error (does not exit).
func Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if inActions {
		log.Printf("::error::%s", msg)
	} else {
		log.Printf("ERROR: %s", msg)
	}
}

// Info logs a plain message (no annotation in either mode).
func Info(format string, args ...any) {
	log.Printf(format, args...)
}

// Preview logs a preview-mode action.
func Preview(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[preview] %s", msg)
}
