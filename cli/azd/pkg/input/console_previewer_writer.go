// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import "log"

// ConsolePreviewerWriter implements io.Writer and is used to wrap a progress log
// and panic if the writer is used after the previewer is stopped.
type consolePreviewerWriter struct {
	// holds the address of a previously created progressLog
	// when the references progressLog becomes nil, this component should write no more.
	previewer **progressLog
}

func (cp *consolePreviewerWriter) Write(logBytes []byte) (int, error) {
	writer := *cp.previewer
	if writer == nil {
		//dev-bug - tried to write to a closed console previewer
		log.Panic("tried to write to a closed console previewer.")
	}

	return writer.Write(logBytes)
}
