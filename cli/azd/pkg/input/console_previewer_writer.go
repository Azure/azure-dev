// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"log"
	syncatomic "sync/atomic"
)

// consolePreviewerWriter implements io.Writer and is used to wrap a progress log.
// Writes are discarded with a log message if the previewer has been stopped (nil).
type consolePreviewerWriter struct {
	// Atomic pointer to the active progressLog. When StopPreviewer nils this out,
	// concurrent writers observe the change without a data race.
	previewer *syncatomic.Pointer[progressLog]
}

func (cp *consolePreviewerWriter) Write(logBytes []byte) (int, error) {
	writer := cp.previewer.Load()
	if writer == nil {
		// The previewer has been stopped. This can happen if a caller writes after all
		// concurrent users have called StopPreviewer (e.g. a lagging goroutine).
		// Gracefully discard the write instead of panicking.
		log.Println("console previewer writer: write after previewer stopped, discarding")
		return len(logBytes), nil
	}

	return writer.Write(logBytes)
}
