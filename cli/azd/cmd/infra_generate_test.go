// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestDetermineDuplicates(t *testing.T) {
	t.Parallel()

	t.Run("no_duplicates", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		// Create files only in source
		require.NoError(t, os.WriteFile(filepath.Join(source, "main.bicep"), []byte("source"), 0600))

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Empty(t, duplicates)
	})

	t.Run("with_duplicates", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		// Create same file in both
		require.NoError(t, os.WriteFile(filepath.Join(source, "main.bicep"), []byte("source"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(target, "main.bicep"), []byte("target"), 0600))

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Equal(t, []string{"main.bicep"}, duplicates)
	})

	t.Run("nested_duplicates", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		// Create nested directory structure
		require.NoError(t, os.MkdirAll(filepath.Join(source, "modules"), 0700))
		require.NoError(t, os.MkdirAll(filepath.Join(target, "modules"), 0700))

		require.NoError(t, os.WriteFile(
			filepath.Join(source, "modules", "storage.bicep"), []byte("s"), 0600))
		require.NoError(t, os.WriteFile(
			filepath.Join(target, "modules", "storage.bicep"), []byte("t"), 0600))

		// Also create a non-duplicate
		require.NoError(t, os.WriteFile(
			filepath.Join(source, "main.bicep"), []byte("s"), 0600))

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Len(t, duplicates, 1)
		assert.Contains(t, duplicates[0], "storage.bicep")
	})

	t.Run("empty_source", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Empty(t, duplicates)
	})
}

func Test_NewInfraGenerateAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &infraGenerateFlags{}
	console := mockinput.NewMockConsole()
	calledAs := CmdCalledAs("infra generate")
	a := newInfraGenerateAction(nil, nil, flags, console, nil, nil, calledAs)
	ia := a.(*infraGenerateAction)
	require.Same(t, flags, ia.flags)
	require.Equal(t, calledAs, ia.calledAs)
}

func Test_DetermineDuplicates_NoDuplicates(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	target := t.TempDir()
	require.NoError(t, os.WriteFile(source+"/file1.bicep", []byte("a"), 0600))
	require.NoError(t, os.WriteFile(source+"/file2.bicep", []byte("b"), 0600))

	dups, err := determineDuplicates(source, target)
	require.NoError(t, err)
	require.Empty(t, dups)
}

func Test_DetermineDuplicates_WithDuplicates(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	target := t.TempDir()
	require.NoError(t, os.WriteFile(source+"/file1.bicep", []byte("a"), 0600))
	require.NoError(t, os.WriteFile(source+"/file2.bicep", []byte("b"), 0600))
	require.NoError(t, os.WriteFile(target+"/file1.bicep", []byte("c"), 0600))

	dups, err := determineDuplicates(source, target)
	require.NoError(t, err)
	require.Len(t, dups, 1)
	require.Contains(t, dups, "file1.bicep")
}

func Test_DetermineDuplicates_AllDuplicates(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	target := t.TempDir()
	require.NoError(t, os.WriteFile(source+"/file1.bicep", []byte("a"), 0600))
	require.NoError(t, os.WriteFile(source+"/file2.bicep", []byte("b"), 0600))
	require.NoError(t, os.WriteFile(target+"/file1.bicep", []byte("c"), 0600))
	require.NoError(t, os.WriteFile(target+"/file2.bicep", []byte("d"), 0600))

	dups, err := determineDuplicates(source, target)
	require.NoError(t, err)
	require.Len(t, dups, 2)
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
				importManager: project.NewImportManager(nil),
				flags:         &infraGenerateFlags{},
				console:       mockinput.NewMockConsole(),
				calledAs:      CmdCalledAs("infra generate"),
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
