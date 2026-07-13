// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"net/http"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteCommand_AcceptsPositionalArg(t *testing.T) {
	cmd := newDeleteCommand(nil)
	err := cmd.Args(cmd, []string{"my-agent"})
	assert.NoError(t, err)
}

func TestDeleteCommand_AcceptsNoArgs(t *testing.T) {
	cmd := newDeleteCommand(nil)
	err := cmd.Args(cmd, []string{})
	assert.NoError(t, err)
}

func TestDeleteCommand_RejectsMultipleArgs(t *testing.T) {
	cmd := newDeleteCommand(nil)
	err := cmd.Args(cmd, []string{"svc1", "svc2"})
	assert.Error(t, err)
}

func TestDeleteCommand_ForceFlag(t *testing.T) {
	cmd := newDeleteCommand(nil)
	flag := cmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("expected --force flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Fatalf("expected --force default false, got %q", flag.DefValue)
	}
}

func TestDeleteCommand_OutputFlagAnnotation(t *testing.T) {
	cmd := newDeleteCommand(nil)
	// RegisterFlagOptions stores allowed values in annotations
	require.NotNil(t, cmd.Annotations)
}

func TestDeleteCommand_VersionFlag(t *testing.T) {
	cmd := newDeleteCommand(nil)
	flag := cmd.Flags().Lookup("version")
	if flag == nil {
		t.Fatal("expected --version flag to be registered")
	}
	if flag.DefValue != "" {
		t.Fatalf("expected --version default empty, got %q", flag.DefValue)
	}
}

// ---------------------------------------------------------------------------
// Error classification tests — calls the real classifyDeleteError from delete.go
// ---------------------------------------------------------------------------

func TestDeleteAgent_404_ProducesValidationError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "not_found",
	}

	result := classifyDeleteError(azErr, "my-agent")
	require.Error(t, result)

	var localErr *azdext.LocalError
	require.True(
		t, errors.As(result, &localErr),
		"404 should produce a LocalError, got: %T", result,
	)
	assert.Equal(t, exterrors.CodeAgentNotFound, localErr.Code)
	assert.Contains(t, localErr.Message, "my-agent")
	assert.Contains(t, localErr.Message, "not found")
}

func TestDeleteAgent_409_ProducesValidationError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusConflict,
		ErrorCode:  "conflict",
	}

	result := classifyDeleteError(azErr, "my-agent")
	require.Error(t, result)

	var localErr *azdext.LocalError
	require.True(
		t, errors.As(result, &localErr),
		"409 should produce a LocalError, got: %T", result,
	)
	assert.Equal(t, exterrors.CodeAgentHasActiveSessions, localErr.Code)
	assert.Contains(t, localErr.Message, "active sessions")
	assert.Contains(t, localErr.Suggestion, "--force")
}

func TestDeleteAgent_500_ProducesServiceError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusInternalServerError,
		ErrorCode:  "internal_error",
	}

	result := classifyDeleteError(azErr, "my-agent")
	require.Error(t, result)

	var svcErr *azdext.ServiceError
	require.True(
		t, errors.As(result, &svcErr),
		"500 should produce a ServiceError, got: %T", result,
	)
}
