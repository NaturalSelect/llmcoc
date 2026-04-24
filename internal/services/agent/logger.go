package agent

import (
	"fmt"
	"log"
	// "os"
	// "strings"
	"time"
)

// debugEnabled is set once at startup from the AGENT_DEBUG environment variable.
// Set AGENT_DEBUG=1 (or "true"/"yes") to enable verbose agent tracing.
var debugEnabled = func() bool {
	return true
}()

// debugf writes a formatted debug line only when AGENT_DEBUG is enabled.
// Format: [agent][TAG] message
func debugf(tag, format string, args ...any) {
	if !debugEnabled {
		return
	}
	msg := fmt.Sprintf(format, args...)
	log.Printf("[agent][%s] %s", tag, msg)
}

// timedDebug returns a function that logs elapsed time when called.
// Usage:
//
//	done := timedDebug("KP", "Chat session=%d iter=%d", sessionID, iter)
//	defer done()
func timedDebug(tag, format string, args ...any) func() {
	if !debugEnabled {
		return func() {}
	}
	label := fmt.Sprintf(format, args...)
	start := time.Now()
	return func() {
		log.Printf("[agent][%s] %s → %.0fms", tag, label, float64(time.Since(start).Microseconds())/1000)
	}
}
