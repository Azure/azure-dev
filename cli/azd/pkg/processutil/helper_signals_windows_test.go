// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package processutil

import "os"

// ignorableSignals lists the signals a helper process must swallow so the escalation
// path can be observed.
//
// Windows never takes the graceful path at all: this package escalates straight to a
// forceful stop, which no process can catch. The helper still registers for interrupt
// so it behaves the same way under a manual Ctrl+C during debugging.
func ignorableSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
