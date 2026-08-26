// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.routines/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestParseRoutineServiceConfig_ServiceLevel(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"description": "nightly summary",
		"enabled":     true,
		"triggers": map[string]any{
			"default": map[string]any{"type": "recurring", "cron_expression": "0 9 * * *"},
		},
		"action": map[string]any{"type": "invoke_agent_responses_api", "agent_name": "summarizer"},
	})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:                 "nightly",
		Host:                 aiRoutineHost,
		AdditionalProperties: props,
	}, "")
	require.NoError(t, err)
	assert.Equal(t, "nightly summary", body.Description)
	require.NotNil(t, body.Enabled)
	assert.True(t, *body.Enabled)
	require.Contains(t, body.Triggers, "default")
	assert.Equal(t, "recurring", body.Triggers["default"].Type)
	assert.Equal(t, "0 9 * * *", body.Triggers["default"].CronExpression)
	require.NotNil(t, body.Action)
	assert.Equal(t, "summarizer", body.Action.AgentName)
}

// TestParseRoutineServiceConfig_ConfigFallback verifies routines written before
// the per-resource service split (config-nested shape) still parse.
func TestParseRoutineServiceConfig_ConfigFallback(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{"description": "legacy"})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:   "legacy",
		Host:   aiRoutineHost,
		Config: props,
	}, "")
	require.NoError(t, err)
	assert.Equal(t, "legacy", body.Description)
}

func TestParseRoutineServiceConfig_FileRef(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "routine.yaml"),
		[]byte("description: referenced routine\n"+
			"triggers:\n"+
			"  default:\n"+
			"    type: schedule\n"+
			"    cron_expression: \"0 2 * * *\"\n"+
			"action:\n"+
			"  type: invoke_agent_responses_api\n"+
			"  agent_name: summarizer\n"+
			"  input:\n"+
			"    $ref: literal-payload-reference\n"+
			"    project: literal-project-value\n"+
			"    instructions: literal-instructions.md\n"),
		0o600,
	))
	props, err := structpb.NewStruct(map[string]any{"$ref": "./routine.yaml"})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:                 "nightly",
		Host:                 aiRoutineHost,
		AdditionalProperties: props,
	}, root)
	require.NoError(t, err)
	assert.Equal(t, "referenced routine", body.Description)
	assert.Equal(t, "0 2 * * *", body.Triggers["default"].CronExpression)
	require.NotNil(t, body.Action)
	assert.Equal(t, "summarizer", body.Action.AgentName)
	assert.Equal(t, map[string]any{
		"$ref":         "literal-payload-reference",
		"project":      "literal-project-value",
		"instructions": "literal-instructions.md",
	}, body.Action.Input)
}

func TestParseRoutineServiceConfig_FileRefOverlay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "routine.yaml"),
		[]byte("description: referenced routine\nenabled: true\n"),
		0o600,
	))
	props, err := structpb.NewStruct(map[string]any{
		"$ref":        "./routine.yaml",
		"description": "inline override",
	})
	require.NoError(t, err)

	body, err := parseRoutineServiceConfig(&azdext.ServiceConfig{
		Name:                 "nightly",
		Host:                 aiRoutineHost,
		AdditionalProperties: props,
	}, root)
	require.NoError(t, err)
	assert.Equal(t, "inline override", body.Description)
	require.NotNil(t, body.Enabled)
	assert.True(t, *body.Enabled)
}

func TestResolveRoutineServiceRef_AbsolutePath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "routine.yaml")
	require.NoError(t, os.WriteFile(path, []byte("description: absolute routine\n"), 0o600))

	resolved, err := resolveRoutineServiceRef(map[string]any{"$ref": path}, "")
	require.NoError(t, err)
	assert.Equal(t, "absolute routine", resolved["description"])
}

func TestResolveRoutineServiceRef_LocalPathWithPercent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "100%-ready.yaml"),
		[]byte("description: percent routine\n"),
		0o600,
	))

	resolved, err := resolveRoutineServiceRef(map[string]any{"$ref": "./100%-ready.yaml"}, root)
	require.NoError(t, err)
	assert.Equal(t, "percent routine", resolved["description"])
}

func TestResolveRoutineServiceRef_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		values      map[string]any
		projectRoot string
		message     string
	}{
		{name: "non-string", values: map[string]any{"$ref": 42}, projectRoot: t.TempDir(), message: "non-empty string"},
		{name: "empty", values: map[string]any{"$ref": "  "}, projectRoot: t.TempDir(), message: "non-empty string"},
		{name: "remote", values: map[string]any{"$ref": "https://example.com/routine.yaml"}, projectRoot: t.TempDir(), message: "not supported"},
		{name: "malformed remote", values: map[string]any{"$ref": "https://example.com/%zz"}, projectRoot: t.TempDir(), message: "not supported"},
		{name: "missing project path", values: map[string]any{"$ref": "./routine.yaml"}, message: "without an azure.yaml project path"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := resolveRoutineServiceRef(test.values, test.projectRoot)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeInvalidRoutineManifest, localErr.Code)
			assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
			assert.Contains(t, localErr.Message, test.message)
			assert.NotEmpty(t, localErr.Suggestion)
		})
	}
}

func TestExpandRoutineValue(t *testing.T) {
	t.Parallel()

	serviceConfig := &azdext.ServiceConfig{
		Environment: map[string]string{"DIGEST_TOPIC": "weekly changes"},
	}
	environment, err := (&routineServiceTarget{}).environmentValues(
		t.Context(),
		serviceConfig,
	)
	require.NoError(t, err)
	input := map[string]any{
		"topic":  "${DIGEST_TOPIC}",
		"secret": "${{connections.search.credentials.key}}",
	}

	assert.Equal(t, map[string]any{
		"topic":  "weekly changes",
		"secret": "${{connections.search.credentials.key}}",
	}, expandRoutineValue(input, environment))
}

// fakeServiceConfigReader reports a fixed env-declared result.
type fakeServiceConfigReader struct {
	found bool
}

func (f fakeServiceConfigReader) Get(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: &azdext.ProjectConfig{}}, nil
}

func (f fakeServiceConfigReader) GetServiceConfigValue(
	context.Context,
	*azdext.GetServiceConfigValueRequest,
	...grpc.CallOption,
) (*azdext.GetServiceConfigValueResponse, error) {
	return &azdext.GetServiceConfigValueResponse{Found: f.found}, nil
}

func TestRoutineEnvironmentValuesEmptyDeclaredIsolates(t *testing.T) {
	t.Parallel()

	target := &routineServiceTarget{
		projectClient: fakeServiceConfigReader{found: true},
	}
	env, err := target.environmentValues(
		t.Context(),
		&azdext.ServiceConfig{Name: "nightly-digest"},
	)
	require.NoError(t, err)
	require.Empty(t, env)
}
