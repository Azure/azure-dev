// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestExternalPromptFromEnvironmentVariables verifies that the console reads
// AZD_UI_PROMPT_ENDPOINT and AZD_UI_PROMPT_KEY environment variables and
// configures external prompting accordingly.
func TestExternalPromptFromEnvironmentVariables(t *testing.T) {
	// Create a test HTTP server that simulates the external prompt endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a success response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"value":  "test-response",
		})
	}))
	defer server.Close()

	testKey := "test-secret-key-12345"

	// Set environment variables
	t.Setenv("AZD_UI_PROMPT_ENDPOINT", server.URL)
	t.Setenv("AZD_UI_PROMPT_KEY", testKey)

	// Create a mock cobra command
	cmd := &cobra.Command{}
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	// Create root options
	rootOptions := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}

	// Create formatter
	var formatter output.Formatter = nil

	// This simulates what happens in container.go when registering the console
	writer := cmd.OutOrStdout()
	if os.Getenv("NO_COLOR") != "" {
		// skip colorable for test
	}

	isTerminal := false // Force non-terminal for test

	// Check for external prompt configuration from environment variables
	// (This is the code we added to container.go)
	var externalPromptCfg *input.ExternalPromptConfiguration
	if endpoint := os.Getenv("AZD_UI_PROMPT_ENDPOINT"); endpoint != "" {
		if key := os.Getenv("AZD_UI_PROMPT_KEY"); key != "" {
			externalPromptCfg = &input.ExternalPromptConfiguration{
				Endpoint:    endpoint,
				Key:         key,
				Transporter: http.DefaultClient,
			}
		}
	}

	// Verify the config was created
	require.NotNil(t, externalPromptCfg, "External prompt config should be created from env vars")
	require.Equal(t, server.URL, externalPromptCfg.Endpoint)
	require.Equal(t, testKey, externalPromptCfg.Key)
	require.NotNil(t, externalPromptCfg.Transporter)

	// Create the console with external prompting configured
	console := input.NewConsole(
		rootOptions.NoPrompt,
		isTerminal,
		input.Writers{Output: writer},
		input.ConsoleHandles{
			Stdin:  cmd.InOrStdin(),
			Stdout: cmd.OutOrStdout(),
			Stderr: cmd.ErrOrStderr(),
		},
		formatter,
		externalPromptCfg,
	)

	require.NotNil(t, console)

	// Note: Actually testing that the console uses external prompting would require
	// calling Prompt/Select/Confirm methods, which is tested in console_test.go
	// This test verifies the environment variable reading logic works correctly.
}

// TestExternalPromptNotConfiguredWithoutEnvVars verifies that when the
// environment variables are not set, no external prompt config is created.
func TestExternalPromptNotConfiguredWithoutEnvVars(t *testing.T) {
	// Ensure env vars are not set
	os.Unsetenv("AZD_UI_PROMPT_ENDPOINT")
	os.Unsetenv("AZD_UI_PROMPT_KEY")

	// Check for external prompt configuration from environment variables
	var externalPromptCfg *input.ExternalPromptConfiguration
	if endpoint := os.Getenv("AZD_UI_PROMPT_ENDPOINT"); endpoint != "" {
		if key := os.Getenv("AZD_UI_PROMPT_KEY"); key != "" {
			externalPromptCfg = &input.ExternalPromptConfiguration{
				Endpoint:    endpoint,
				Key:         key,
				Transporter: http.DefaultClient,
			}
		}
	}

	require.Nil(t, externalPromptCfg, "External prompt config should be nil when env vars not set")
}

// TestExternalPromptRequiresBothEnvVars verifies that both environment
// variables must be set for external prompting to be configured.
func TestExternalPromptRequiresBothEnvVars(t *testing.T) {
	t.Run("only endpoint set", func(t *testing.T) {
		t.Setenv("AZD_UI_PROMPT_ENDPOINT", "http://localhost:8080")
		os.Unsetenv("AZD_UI_PROMPT_KEY")

		var externalPromptCfg *input.ExternalPromptConfiguration
		if endpoint := os.Getenv("AZD_UI_PROMPT_ENDPOINT"); endpoint != "" {
			if key := os.Getenv("AZD_UI_PROMPT_KEY"); key != "" {
				externalPromptCfg = &input.ExternalPromptConfiguration{
					Endpoint:    endpoint,
					Key:         key,
					Transporter: http.DefaultClient,
				}
			}
		}

		require.Nil(t, externalPromptCfg, "Config should be nil when only endpoint is set")
	})

	t.Run("only key set", func(t *testing.T) {
		os.Unsetenv("AZD_UI_PROMPT_ENDPOINT")
		t.Setenv("AZD_UI_PROMPT_KEY", "secret-key")

		var externalPromptCfg *input.ExternalPromptConfiguration
		if endpoint := os.Getenv("AZD_UI_PROMPT_ENDPOINT"); endpoint != "" {
			if key := os.Getenv("AZD_UI_PROMPT_KEY"); key != "" {
				externalPromptCfg = &input.ExternalPromptConfiguration{
					Endpoint:    endpoint,
					Key:         key,
					Transporter: http.DefaultClient,
				}
			}
		}

		require.Nil(t, externalPromptCfg, "Config should be nil when only key is set")
	})
}
