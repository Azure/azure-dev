// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"strings"
	"testing"
	"time"

	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_UxMiddleware_PromptTimeout(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewConfig(nil)
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)

	middleware := NewUxMiddleware(
		&Options{
			Name:        "provision",
			CommandPath: "azd provision",
		},
		mockContext.Console,
		featureManager,
	)

	// Create prompt timeout error
	timeoutErr := &ux.ErrPromptTimeout{
		Duration: 30 * time.Second,
	}
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, timeoutErr
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	require.Error(t, err)
	require.Nil(t, result)

	// Check that suggestion was displayed with --no-prompt
	consoleOutput := mockContext.Console.Output()
	foundSuggestion := false
	for _, message := range consoleOutput {
		if strings.Contains(message, "--no-prompt") && strings.Contains(message, "azd provision") {
			foundSuggestion = true
			break
		}
	}
	require.True(t, foundSuggestion, "No --no-prompt suggestion displayed for ErrPromptTimeout")
}

func Test_UxMiddleware_PromptTimeout_NestedCommand(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cfg := config.NewConfig(nil)
	featureManager := alpha.NewFeaturesManagerWithConfig(cfg)

	middleware := NewUxMiddleware(
		&Options{
			Name:        "list",
			CommandPath: "azd env list",
		},
		mockContext.Console,
		featureManager,
	)

	// Create prompt timeout error
	timeoutErr := &ux.ErrPromptTimeout{
		Duration: 60 * time.Second,
	}
	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, timeoutErr
	}

	result, err := middleware.Run(*mockContext.Context, nextFn)

	require.Error(t, err)
	require.Nil(t, result)

	// Check that the full command path is in the suggestion
	consoleOutput := mockContext.Console.Output()
	foundSuggestion := false
	for _, message := range consoleOutput {
		if strings.Contains(message, "azd env list --no-prompt") {
			foundSuggestion = true
			break
		}
	}
	require.True(t, foundSuggestion, "Full command path not in --no-prompt suggestion")
}
