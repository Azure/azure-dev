// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewInfraGenerateAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &infraGenerateFlags{}
	console := mockinput.NewMockConsole()
	calledAs := CmdCalledAs("infra generate")
	a := newInfraGenerateAction(nil, nil, nil, flags, console, nil, nil, calledAs)
	ia := a.(*infraGenerateAction)
	require.Same(t, flags, ia.flags)
	require.Equal(t, calledAs, ia.calledAs)
}

func Test_ConfirmOverwrite(t *testing.T) {
	t.Parallel()

	// newAction returns an action for a built-in project whose infra folder optionally contains a file.
	newAction := func(t *testing.T, existing bool) (*infraGenerateAction, *mockinput.MockConsole) {
		dir := t.TempDir()
		if existing {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "infra", "unrelated.txt"), []byte("old"), 0600))
		}

		console := mockinput.NewMockConsole()
		return &infraGenerateAction{
			projectConfig:     &project.ProjectConfig{Path: dir},
			generationManager: project.NewInfraGenerationManager(project.NewImportManager(nil), nil),
			azdCtx:            azdcontext.NewAzdContextWithDirectory(dir),
			console:           console,
		}, console
	}

	t.Run("empty_folder_does_not_prompt", func(t *testing.T) {
		t.Parallel()
		// No Confirm expectation is registered, so a prompt would fail the test.
		a, _ := newAction(t, false)
		overwrite, err := a.confirmOverwrite(t.Context())
		require.NoError(t, err)
		require.False(t, overwrite)
	})

	t.Run("accept", func(t *testing.T) {
		t.Parallel()
		a, console := newAction(t, true)
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
		overwrite, err := a.confirmOverwrite(t.Context())
		require.NoError(t, err)
		require.True(t, overwrite)
	})

	t.Run("decline", func(t *testing.T) {
		t.Parallel()
		a, console := newAction(t, true)
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(false)
		_, err := a.confirmOverwrite(t.Context())
		require.ErrorIs(t, err, internal.ErrAbortedByUser)
	})

	t.Run("no_prompt", func(t *testing.T) {
		t.Parallel()
		a, console := newAction(t, true)
		console.SetNoPromptMode(true)

		_, err := a.confirmOverwrite(t.Context())
		suggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
		require.True(t, ok, "expected ErrorWithSuggestion, got %v", err)
		require.Contains(t, suggestion.Suggestion, "--force")
	})
}

func Test_NewInfraGenerateFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newInfraGenerateFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewInfraGenerateCmd(t *testing.T) {
	t.Parallel()
	cmd := newInfraGenerateCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "generate")
}

// Test_InfraGenerateAction_RecordsInfraProvider exercises the action end-to-end (not just the
// mapper in isolation) to verify the infra.provider telemetry contract for `infra generate` /
// `synth`: an unset provider is emitted as "auto", a built-in provider is emitted verbatim, and a
// non-built-in (extension) provider is bucketed to the scalar "custom" so the raw configured name
// is never emitted. It also asserts the attribute is a scalar string (not a slice) on the command
// span, which the manager-level mapper test cannot catch.
func Test_InfraGenerateAction_RecordsInfraProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider provisioning.ProviderKind
		expected string
	}{
		{name: "unset resolves to auto", provider: provisioning.NotSpecified, expected: "auto"},
		{name: "built-in bicep verbatim", provider: provisioning.Bicep, expected: "bicep"},
		{name: "built-in terraform verbatim", provider: provisioning.Terraform, expected: "terraform"},
		{name: "built-in arm verbatim", provider: provisioning.Arm, expected: "arm"},
		{name: "built-in pulumi verbatim", provider: provisioning.Pulumi, expected: "pulumi"},
		{
			name:     "extension provider bucketed to custom",
			provider: provisioning.ProviderKind("my-extension-provider"),
			expected: provisioning.InfraProviderCustom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Record onto a real span captured by an in-memory recorder so the test verifies the
			// attribute lands directly on the command span with the expected scalar shape.
			sr := tracetest.NewSpanRecorder()
			tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(sr))
			ctx, span := tp.Tracer("test").Start(t.Context(), "cmd.infra.generate")

			action := &infraGenerateAction{
				projectConfig: &project.ProjectConfig{
					Infra: provisioning.Options{Provider: tt.provider},
				},
				generationManager:  project.NewInfraGenerationManager(project.NewImportManager(nil), nil),
				extensionActivator: &actionGenerationActivator{},
				azdCtx:             azdcontext.NewAzdContextWithDirectory(t.TempDir()),
				flags:              &infraGenerateFlags{EnvFlag: &internal.EnvFlag{}},
				console:            mockinput.NewMockConsole(),
				calledAs:           CmdCalledAs("infra generate"),
			}

			// The empty project has no infrastructure to generate, so Run returns an error after the
			// telemetry attribute is already recorded — exactly the ordering we want to assert.
			_, err := action.Run(ctx)
			require.Error(t, err)
			span.End()

			ended := sr.Ended()
			require.Len(t, ended, 1)

			var attr attribute.KeyValue
			var found bool
			for _, a := range ended[0].Attributes() {
				if a.Key == fields.InfraProviderKey.Key {
					attr = a
					found = true
				}
			}

			require.True(t, found, "expected infra.provider attribute to be recorded")
			require.Equal(t, attribute.STRING, attr.Value.Type(), "infra.provider must be a scalar string")
			require.Equal(t, tt.expected, attr.Value.AsString())
		})
	}
}
