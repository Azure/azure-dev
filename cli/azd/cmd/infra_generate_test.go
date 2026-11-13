// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

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

type failingGenerationFS struct {
	fs.FS
	err error
}

func (f failingGenerationFS) Open(name string) (fs.File, error) {
	if name == "z.bicep" {
		return nil, f.err
	}
	return f.FS.Open(name)
}

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

func Test_ConfirmExtensionOverwrite(t *testing.T) {
	t.Parallel()

	// newAction returns an action for an extension project whose output folder optionally contains a file.
	newAction := func(t *testing.T, existing bool) (*infraGenerateAction, *mockinput.MockConsole) {
		dir := t.TempDir()
		if existing {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "infra", "unrelated.txt"), []byte("old"), 0600))
		}

		console := mockinput.NewMockConsole()
		azdCtx := azdcontext.NewAzdContextWithDirectory(dir)
		return &infraGenerateAction{
			projectConfig: &project.ProjectConfig{
				Path: dir,
				Infra: provisioning.Options{
					Provider: provisioning.ProviderKind("resources"),
					Name:     "data",
					Path:     "infra",
				},
			},
			generationManager: project.NewInfraGenerationManager(project.NewImportManager(nil), nil, azdCtx),
			azdCtx:            azdCtx,
			console:           console,
		}, console
	}

	t.Run("empty_folder_does_not_prompt", func(t *testing.T) {
		t.Parallel()
		// No Confirm expectation is registered, so a prompt would fail the test.
		a, _ := newAction(t, false)
		overwrite, err := a.confirmExtensionOverwrite(t.Context())
		require.NoError(t, err)
		require.False(t, overwrite)
	})

	t.Run("accept", func(t *testing.T) {
		t.Parallel()
		a, console := newAction(t, true)
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
		overwrite, err := a.confirmExtensionOverwrite(t.Context())
		require.NoError(t, err)
		require.True(t, overwrite)
	})

	t.Run("decline", func(t *testing.T) {
		t.Parallel()
		a, console := newAction(t, true)
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(false)
		_, err := a.confirmExtensionOverwrite(t.Context())
		require.ErrorIs(t, err, internal.ErrAbortedByUser)
	})

	t.Run("no_prompt", func(t *testing.T) {
		t.Parallel()
		a, console := newAction(t, true)
		console.SetNoPromptMode(true)

		_, err := a.confirmExtensionOverwrite(t.Context())
		suggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
		require.True(t, ok, "expected ErrorWithSuggestion, got %v", err)
		require.Contains(t, suggestion.Suggestion, "--force")
	})
}

func TestFolderEmpty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	emptyDir := filepath.Join(root, "empty")
	require.NoError(t, os.Mkdir(emptyDir, 0700))
	nonEmptyDir := filepath.Join(root, "non-empty")
	require.NoError(t, os.Mkdir(nonEmptyDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(nonEmptyDir, "file"), []byte("content"), 0600))

	tests := []struct {
		name string
		dir  string
		want bool
	}{
		{name: "missing directory", dir: filepath.Join(root, "missing"), want: true},
		{name: "empty directory", dir: emptyDir, want: true},
		{name: "non-empty directory", dir: nonEmptyDir, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			empty, err := folderEmpty(tt.dir)
			require.NoError(t, err)
			require.Equal(t, tt.want, empty)
		})
	}
}

func Test_WriteLegacyGeneratedFiles(t *testing.T) {
	t.Parallel()

	// Mirrors Aspire output, which writes a service manifest outside infra.path.
	const manifest = "api/infra/api.tmpl.yaml"
	synthFS := fstest.MapFS{
		"infra/main.bicep": {Data: []byte("generated")},
		manifest:           {Data: []byte("generated")},
	}

	// newAction returns an action whose project folder contains the given files.
	newAction := func(
		t *testing.T, force bool, files map[string]string,
	) (*infraGenerateAction, *mockinput.MockConsole, *os.Root) {
		dir := t.TempDir()
		for name, contents := range files {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600))
		}

		root, err := os.OpenRoot(dir)
		require.NoError(t, err)
		t.Cleanup(func() { root.Close() })

		console := mockinput.NewMockConsole()
		return &infraGenerateAction{console: console, flags: &infraGenerateFlags{force: force}}, console, root
	}

	readFile := func(t *testing.T, root *os.Root, name string) string {
		contents, err := root.ReadFile(name)
		require.NoError(t, err)
		return string(contents)
	}

	t.Run("keep_existing_file_outside_infra_path", func(t *testing.T) {
		t.Parallel()
		a, console, root := newAction(t, false, map[string]string{manifest: "custom"})
		console.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(1)

		require.NoError(t, a.writeLegacyGeneratedFiles(t.Context(), root.Name(), synthFS))
		require.Equal(t, "custom", readFile(t, root, manifest))
		require.Equal(t, "generated", readFile(t, root, "infra/main.bicep"))
	})

	t.Run("overwrite_existing_file_outside_infra_path", func(t *testing.T) {
		t.Parallel()
		a, console, root := newAction(t, false, map[string]string{manifest: "custom"})
		console.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(0)

		require.NoError(t, a.writeLegacyGeneratedFiles(t.Context(), root.Name(), synthFS))
		require.Equal(t, "generated", readFile(t, root, manifest))
	})

	t.Run("force_overwrites_without_prompting", func(t *testing.T) {
		t.Parallel()
		// No Select expectation is registered, so a prompt would fail the test.
		a, _, root := newAction(t, true, map[string]string{manifest: "custom"})

		require.NoError(t, a.writeLegacyGeneratedFiles(t.Context(), root.Name(), synthFS))
		require.Equal(t, "generated", readFile(t, root, manifest))
	})

	t.Run("no_prompt_existing_file_errors", func(t *testing.T) {
		t.Parallel()
		a, console, root := newAction(t, false, map[string]string{manifest: "custom"})
		console.SetNoPromptMode(true)

		err := a.writeLegacyGeneratedFiles(t.Context(), root.Name(), synthFS)
		suggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
		require.True(t, ok, "expected ErrorWithSuggestion, got %v", err)
		require.Contains(t, suggestion.Suggestion, "--force")
		require.Equal(t, "custom", readFile(t, root, manifest))
		require.NoFileExists(t, filepath.Join(root.Name(), "infra", "main.bicep"))
	})

	t.Run("no_prompt_ignores_unrelated_files", func(t *testing.T) {
		t.Parallel()
		a, console, root := newAction(t, false, map[string]string{"infra/unrelated.txt": "old"})
		console.SetNoPromptMode(true)

		require.NoError(t, a.writeLegacyGeneratedFiles(t.Context(), root.Name(), synthFS))
		require.Equal(t, "generated", readFile(t, root, manifest))
		require.Equal(t, "old", readFile(t, root, "infra/unrelated.txt"))
	})
}

func Test_LegacyGenerationReadFailureLeavesProjectUnchanged(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := filepath.Join(dir, "a.bicep")
	require.NoError(t, os.WriteFile(existing, []byte("original"), 0600))
	readErr := errors.New("generated file read failed")
	generated := failingGenerationFS{
		FS: fstest.MapFS{
			"a.bicep": {Data: []byte("replacement")},
			"z.bicep": {Data: []byte("new")},
		},
		err: readErr,
	}
	action := &infraGenerateAction{flags: &infraGenerateFlags{force: true}}

	err := action.writeLegacyGeneratedFiles(t.Context(), dir, generated)
	require.ErrorIs(t, err, readErr)
	contents, err := os.ReadFile(existing)
	require.NoError(t, err)
	require.Equal(t, "original", string(contents))
	require.NoFileExists(t, filepath.Join(dir, "z.bicep"))
}

func Test_LegacyGenerationCancellationLeavesProjectUnchanged(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	action := &infraGenerateAction{flags: &infraGenerateFlags{force: true}}

	err := action.writeLegacyGeneratedFiles(ctx, dir, fstest.MapFS{"main.bicep": {Data: []byte("generated")}})
	require.ErrorIs(t, err, context.Canceled)
	require.NoFileExists(t, filepath.Join(dir, "main.bicep"))
}

func Test_InfraGenerateBuiltInIgnoresUnrelatedFilesWithoutPrompt(t *testing.T) {
	t.Parallel()

	config, err := project.Parse(t.Context(), "name: example\nresources:\n  cache:\n    type: db.redis\n")
	require.NoError(t, err)
	config.Path = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(config.Path, "infra"), 0700))
	unrelatedFile := filepath.Join(config.Path, "infra", "notes.txt")
	require.NoError(t, os.WriteFile(unrelatedFile, []byte("keep"), 0600))

	console := mockinput.NewMockConsole()
	console.SetNoPromptMode(true)
	azdCtx := azdcontext.NewAzdContextWithDirectory(config.Path)
	action := &infraGenerateAction{
		projectConfig:     config,
		generationManager: project.NewInfraGenerationManager(project.NewImportManager(nil), nil, azdCtx),
		azdCtx:            azdCtx,
		flags:             &infraGenerateFlags{EnvFlag: &internal.EnvFlag{}},
		console:           console,
	}

	_, err = action.Run(t.Context())
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(config.Path, "infra", "main.bicep"))
	contents, err := os.ReadFile(unrelatedFile)
	require.NoError(t, err)
	require.Equal(t, "keep", string(contents))
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

			azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
			action := &infraGenerateAction{
				projectConfig: &project.ProjectConfig{
					Infra: provisioning.Options{Provider: tt.provider},
				},
				generationManager:  project.NewInfraGenerationManager(project.NewImportManager(nil), nil, azdCtx),
				extensionActivator: &actionGenerationActivator{},
				azdCtx:             azdCtx,
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
