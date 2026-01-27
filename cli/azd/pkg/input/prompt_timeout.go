// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/ux"
)

// PromptTimeoutEnvVar is the environment variable name for configuring prompt timeout.
const PromptTimeoutEnvVar = "AZD_PROMPT_TIMEOUT"

// DefaultPromptTimeout is the default timeout duration when AZD_PROMPT_TIMEOUT is not set.
const DefaultPromptTimeout = 30 * time.Second

// GetPromptTimeout returns the configured prompt timeout duration.
// It reads the AZD_PROMPT_TIMEOUT environment variable and parses it as seconds.
// Returns DefaultPromptTimeout (30s) if the variable is empty or invalid.
// Returns 0 (disabled) only if explicitly set to 0.
func GetPromptTimeout() time.Duration {
	value := os.Getenv(PromptTimeoutEnvVar)
	if value == "" {
		return DefaultPromptTimeout
	}

	seconds, err := strconv.Atoi(value)
	if err != nil {
		return DefaultPromptTimeout
	}

	if seconds <= 0 {
		return 0
	}

	return time.Duration(seconds) * time.Second
}

// WithPromptTimeout wraps the context with a timeout if AZD_PROMPT_TIMEOUT is configured.
// Returns the context (potentially with timeout) and a cancel function that must be deferred.
func WithPromptTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := GetPromptTimeout()
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// HandlePromptError checks if err is a context deadline error and converts it to ErrPromptTimeout.
// Returns the original error unchanged for all other error types.
func HandlePromptError(err error) error {
	if err == nil {
		return nil
	}

	timeout := GetPromptTimeout()
	if timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
		return &ux.ErrPromptTimeout{Duration: timeout}
	}
	return err
}
