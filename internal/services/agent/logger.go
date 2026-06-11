// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"fmt"
	"log"
	// "os"
	// "strings"
	"time"
)

// debugf writes a formatted debug line only when AGENT_DEBUG is enabled.
// Format: [agent][TAG] message
func debugf(tag, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[agent][%s] %s", tag, msg)
}

// timedDebug returns a function that logs elapsed time when called.
// Usage:
//
//	done := timedDebug("KP", "Chat session=%d iter=%d", sessionID, iter)
//	defer done()
func timedDebug(tag, format string, args ...any) func() {
	label := fmt.Sprintf(format, args...)
	start := time.Now()
	return func() {
		log.Printf("[agent][%s] %s → %.0fms", tag, label, float64(time.Since(start).Microseconds())/1000)
	}
}
