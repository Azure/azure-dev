// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/ux"
)

func TestGetPromptTimeout(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected time.Duration
	}{
		{
			name:     "empty value returns default 30s",
			envValue: "",
			expected: DefaultPromptTimeout,
		},
		{
			name:     "valid positive integer",
			envValue: "60",
			expected: 60 * time.Second,
		},
		{
			name:     "zero returns 0 (disabled)",
			envValue: "0",
			expected: 0,
		},
		{
			name:     "negative value returns 0 (disabled)",
			envValue: "-5",
			expected: 0,
		},
		{
			name:     "invalid non-numeric returns default 30s",
			envValue: "abc",
			expected: DefaultPromptTimeout,
		},
		{
			name:     "float value returns default 30s (only integers)",
			envValue: "30.5",
			expected: DefaultPromptTimeout,
		},
		{
			name:     "large valid value",
			envValue: "300",
			expected: 300 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore original value
			original := os.Getenv(PromptTimeoutEnvVar)
			defer os.Setenv(PromptTimeoutEnvVar, original)

			if tt.envValue == "" {
				os.Unsetenv(PromptTimeoutEnvVar)
			} else {
				os.Setenv(PromptTimeoutEnvVar, tt.envValue)
			}

			result := GetPromptTimeout()
			if result != tt.expected {
				t.Errorf("GetPromptTimeout() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestWithPromptTimeout(t *testing.T) {
	t.Run("timeout disabled with 0 returns original context", func(t *testing.T) {
		original := os.Getenv(PromptTimeoutEnvVar)
		defer os.Setenv(PromptTimeoutEnvVar, original)
		os.Setenv(PromptTimeoutEnvVar, "0")

		ctx := context.Background()
		newCtx, cancel := WithPromptTimeout(ctx)
		defer cancel()

		// Should return same context (no deadline)
		_, hasDeadline := newCtx.Deadline()
		if hasDeadline {
			t.Error("expected no deadline when timeout is disabled")
		}
	})

	t.Run("default timeout adds deadline to context", func(t *testing.T) {
		original := os.Getenv(PromptTimeoutEnvVar)
		defer os.Setenv(PromptTimeoutEnvVar, original)
		os.Unsetenv(PromptTimeoutEnvVar)

		ctx := context.Background()
		newCtx, cancel := WithPromptTimeout(ctx)
		defer cancel()

		deadline, hasDeadline := newCtx.Deadline()
		if !hasDeadline {
			t.Error("expected deadline when using default timeout")
		}

		// Deadline should be approximately 30 seconds from now
		expectedDeadline := time.Now().Add(DefaultPromptTimeout)
		if deadline.Before(expectedDeadline.Add(-1*time.Second)) || deadline.After(expectedDeadline.Add(1*time.Second)) {
			t.Errorf("deadline %v not within expected range around %v", deadline, expectedDeadline)
		}
	})

	t.Run("custom timeout adds deadline to context", func(t *testing.T) {
		original := os.Getenv(PromptTimeoutEnvVar)
		defer os.Setenv(PromptTimeoutEnvVar, original)
		os.Setenv(PromptTimeoutEnvVar, "60")

		ctx := context.Background()
		newCtx, cancel := WithPromptTimeout(ctx)
		defer cancel()

		deadline, hasDeadline := newCtx.Deadline()
		if !hasDeadline {
			t.Error("expected deadline when timeout is configured")
		}

		// Deadline should be approximately 60 seconds from now
		expectedDeadline := time.Now().Add(60 * time.Second)
		if deadline.Before(expectedDeadline.Add(-1*time.Second)) || deadline.After(expectedDeadline.Add(1*time.Second)) {
			t.Errorf("deadline %v not within expected range around %v", deadline, expectedDeadline)
		}
	})
}

func TestHandlePromptError(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		result := HandlePromptError(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("non-deadline error returns unchanged", func(t *testing.T) {
		original := os.Getenv(PromptTimeoutEnvVar)
		defer os.Setenv(PromptTimeoutEnvVar, original)
		os.Setenv(PromptTimeoutEnvVar, "30")

		origErr := errors.New("some other error")
		result := HandlePromptError(origErr)
		if !errors.Is(result, origErr) {
			t.Errorf("expected original error, got %v", result)
		}
	})

	t.Run("deadline error with timeout configured converts to ErrPromptTimeout", func(t *testing.T) {
		original := os.Getenv(PromptTimeoutEnvVar)
		defer os.Setenv(PromptTimeoutEnvVar, original)
		os.Setenv(PromptTimeoutEnvVar, "60")

		result := HandlePromptError(context.DeadlineExceeded)

		var promptTimeout *ux.ErrPromptTimeout
		if !errors.As(result, &promptTimeout) {
			t.Errorf("expected ErrPromptTimeout, got %T", result)
		}

		if promptTimeout.Duration != 60*time.Second {
			t.Errorf("expected duration 60s, got %v", promptTimeout.Duration)
		}
	})

	t.Run("deadline error with default timeout converts to ErrPromptTimeout", func(t *testing.T) {
		original := os.Getenv(PromptTimeoutEnvVar)
		defer os.Setenv(PromptTimeoutEnvVar, original)
		os.Unsetenv(PromptTimeoutEnvVar)

		result := HandlePromptError(context.DeadlineExceeded)

		var promptTimeout *ux.ErrPromptTimeout
		if !errors.As(result, &promptTimeout) {
			t.Errorf("expected ErrPromptTimeout, got %T", result)
		}

		if promptTimeout.Duration != DefaultPromptTimeout {
			t.Errorf("expected duration %v, got %v", DefaultPromptTimeout, promptTimeout.Duration)
		}
	})

	t.Run("deadline error with timeout disabled returns unchanged", func(t *testing.T) {
		original := os.Getenv(PromptTimeoutEnvVar)
		defer os.Setenv(PromptTimeoutEnvVar, original)
		os.Setenv(PromptTimeoutEnvVar, "0")

		result := HandlePromptError(context.DeadlineExceeded)
		if !errors.Is(result, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded, got %v", result)
		}
	})
}
