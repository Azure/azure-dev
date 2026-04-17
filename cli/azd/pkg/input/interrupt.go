// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import "sync"

// InterruptHandler is invoked when the user presses Ctrl+C.
//
// Implementations are expected to drive any user interaction (such as
// confirming whether to abort an in-flight Azure operation) and return only
// after they have decided how to respond. The handler runs synchronously on a
// dedicated goroutine: any additional Ctrl+C signals received while the
// handler is running are ignored.
//
// If the returned bool is false, the default azd interrupt behavior runs after
// the handler returns (the spinner is stopped and the process exits with
// code 1). Returning true tells the runtime that the handler took ownership of
// the shutdown sequence.
type InterruptHandler func() (handled bool)

var (
	interruptMu      sync.Mutex
	interruptStack   []InterruptHandler
	interruptRunning bool
)

// PushInterruptHandler registers a handler to be invoked on the next SIGINT
// (Ctrl+C). Handlers are stacked: the most recently pushed handler runs first.
//
// The returned function pops the handler from the stack and must be called to
// restore the previous interrupt behavior (typically with `defer`).
func PushInterruptHandler(h InterruptHandler) func() {
	interruptMu.Lock()
	interruptStack = append(interruptStack, h)
	idx := len(interruptStack) - 1
	interruptMu.Unlock()

	return func() {
		interruptMu.Lock()
		defer interruptMu.Unlock()
		// Trim the stack back to the position before this handler was pushed.
		// We do not search by identity because handlers are pushed/popped in
		// LIFO order via defer; truncating to idx is correct.
		if idx < len(interruptStack) {
			interruptStack = interruptStack[:idx]
		}
	}
}

// currentInterruptHandler returns the top-of-stack interrupt handler, or nil
// if no handler is registered.
func currentInterruptHandler() InterruptHandler {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	if len(interruptStack) == 0 {
		return nil
	}
	return interruptStack[len(interruptStack)-1]
}

// tryStartInterruptHandler returns true if no handler is currently running.
// On success the caller is responsible for calling finishInterruptHandler.
func tryStartInterruptHandler() bool {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	if interruptRunning {
		return false
	}
	interruptRunning = true
	return true
}

func finishInterruptHandler() {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	interruptRunning = false
}
