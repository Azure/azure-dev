// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"sync"
	"time"
)

// globalConfig holds the global UX configuration.
var globalConfig = &config{}
var configMu sync.RWMutex

// config holds UX library configuration options.
type config struct {
	// PromptTimeout is the duration after which prompts will automatically timeout.
	// When set to 0 or negative, no timeout is applied.
	PromptTimeout time.Duration
}

// SetPromptTimeout sets the global prompt timeout duration.
// When set to a positive value, all UX prompts will automatically timeout after this duration.
// Set to 0 to disable timeout.
func SetPromptTimeout(timeout time.Duration) {
	configMu.Lock()
	defer configMu.Unlock()
	globalConfig.PromptTimeout = timeout
}

// GetPromptTimeout returns the configured global prompt timeout duration.
// Returns 0 if timeout is disabled.
func GetPromptTimeout() time.Duration {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig.PromptTimeout
}

// ErrPromptTimeout is returned when an interactive prompt times out waiting for user input.
// This typically occurs when AZD_PROMPT_TIMEOUT is configured and the user does not respond
// within the specified duration.
type ErrPromptTimeout struct {
	Duration time.Duration
}

// Error returns the error message
func (e *ErrPromptTimeout) Error() string {
	return fmt.Sprintf("prompt timed out after %d seconds", int(e.Duration.Seconds()))
}
