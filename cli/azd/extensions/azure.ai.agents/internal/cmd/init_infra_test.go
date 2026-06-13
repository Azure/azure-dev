// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withCapturedStdout replaces os.Stdout with a pipe for the duration of fn,
// returns everything the function wrote, and restores stdout afterward.
// Pattern mirrors endpoint_show_test.go in the same package.
func withCapturedStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err, "create stdout pipe")
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		_, _ = io.Copy(&sb, r)
		done <- sb.String()
	}()

	defer func() {
		os.Stdout = orig
	}()

	fn()
	_ = w.Close()
	return <-done
}

// validFoundryAzureYAML returns an azure.yaml payload exercising the
// synthesizer's two derived parameters: deployments and includeAcr.
// Container deployment via the `docker:` block forces includeAcr=true.
const validFoundryAzureYAML = `name: my-project
metadata:
  template: azure.ai.agents
infra:
  provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.agent
    deployments:
      - name: gpt-4-1-mini
        model:
          name: gpt-4.1-mini
          format: OpenAI
          version: "2024-07-18"
        sku:
          name: GlobalStandard
          capacity: 10
    agents:
      - name: my-agent
        docker:
          path: src/my-agent
`

func TestEjectInfra_RefusesWhenAzureYamlMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := ejectInfra(dir)
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured azdext.LocalError, got %T: %v", err, err)
	assert.Equal(t, exterrors.CodeInfraEjectAzureYamlMissing, localErr.Code)
	assert.Contains(t, localErr.Message, "azure.yaml not found")
	assert.NotEmpty(t, localErr.Suggestion)

	// Refusal must not produce ./infra/.
	_, statErr := os.Stat(filepath.Join(dir, "infra"))
	assert.True(t, os.IsNotExist(statErr), "infra/ must not be created on refusal")
}

func TestEjectInfra_RefusesWhenInfraExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	// Pre-create infra/ -- contents don't matter, even an empty dir refuses.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))

	err := ejectInfra(dir)
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	assert.Contains(t, localErr.Message, "./infra/")
	assert.Contains(t, localErr.Suggestion, "delete the infra directory")

	// Pre-existing infra/ must not be wiped by the refusal.
	info, err := os.Stat(filepath.Join(dir, "infra"))
	require.NoError(t, err, "pre-existing infra/ must survive refusal")
	assert.True(t, info.IsDir())
}

func TestEjectInfra_RefusesWhenNoFoundryService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "non-foundry services only",
			yaml: `name: my-project
services:
  webapp:
    host: containerapp
    project: src/web
`,
		},
		{
			name: "no services block at all",
			yaml: `name: my-project
infra:
  provider: microsoft.foundry
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, "azure.yaml"), tt.yaml)

			err := ejectInfra(dir)
			require.Error(t, err)

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected structured azdext.LocalError, got %T", err)
			assert.Equal(t, exterrors.CodeInfraEjectNoFoundryService, localErr.Code)
			assert.Contains(t, localErr.Message, "nothing to eject")

			_, statErr := os.Stat(filepath.Join(dir, "infra"))
			assert.True(t, os.IsNotExist(statErr), "infra/ must not be created on refusal")
		})
	}
}

func TestEjectInfra_RefusesWhenMultipleFoundryServices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  agent-a:
    host: azure.ai.agent
  agent-b:
    host: azure.ai.agent
`)

	err := ejectInfra(dir)
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectMultipleFoundryServices, localErr.Code)
	assert.Contains(t, localErr.Message, "multiple services")
	// Deterministic ordering check: matches are sorted before formatting.
	assert.Contains(t, localErr.Message, "[agent-a agent-b]")
}

func TestEjectInfra_HappyPath_WritesExpectedFiles(t *testing.T) {
	// Intentionally NOT parallel: this test captures os.Stdout, and running
	// it concurrently with other stdout-capturing tests in the same package
	// would race over the global file descriptor.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	stdout := withCapturedStdout(t, func() {
		err := ejectInfra(dir)
		require.NoError(t, err)
	})

	// Every embedded template under templates/ (except main.arm.json) should
	// be on disk under ./infra/, plus the synthesized main.parameters.json.
	expected := []string{
		filepath.Join("infra", "main.bicep"),
		filepath.Join("infra", "abbreviations.json"),
		filepath.Join("infra", "modules", "acr.bicep"),
		filepath.Join("infra", "main.parameters.json"),
	}
	for _, rel := range expected {
		path := filepath.Join(dir, rel)
		info, err := os.Stat(path)
		require.NoError(t, err, "expected file %s", rel)
		assert.Greater(t, info.Size(), int64(0), "file %s should not be empty", rel)
	}

	// main.arm.json is deliberately excluded.
	_, err := os.Stat(filepath.Join(dir, "infra", "main.arm.json"))
	assert.True(t, os.IsNotExist(err),
		"main.arm.json should be excluded from the ejected tree (it would be stale "+
			"the moment the user edits main.bicep)")

	// Spec's success block elements.
	assert.Contains(t, stdout, "Generating infrastructure files from azure.yaml")
	assert.Contains(t, stdout, "infra/main.bicep")
	assert.Contains(t, stdout, "infra/modules/acr.bicep")
	assert.Contains(t, stdout, "infra/main.parameters.json")
	assert.Contains(t, stdout, "Future provisions will read from ./infra/")
	assert.Contains(t, stdout, "Next steps:")
	assert.Contains(t, stdout, "azd provision")

	// azure.yaml must not be mutated by eject (spec is explicit on this).
	got, err := os.ReadFile(filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	assert.Equal(t, validFoundryAzureYAML, string(got),
		"azure.yaml must not be mutated by eject")
}

func TestEjectInfra_HappyPath_ParametersFileShape(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json"))
	require.NoError(t, err)

	var doc struct {
		Schema         string `json:"$schema"`
		ContentVersion string `json:"contentVersion"`
		Parameters     map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc),
		"main.parameters.json must be valid JSON")

	assert.Contains(t, doc.Schema, "deploymentParameters.json",
		"$schema must point at the ARM parameters schema")
	assert.Equal(t, "1.0.0.0", doc.ContentVersion)

	// Synthesizer derives exactly these two from the test YAML: includeAcr
	// because of the docker: block, and a single deployment entry.
	require.Contains(t, doc.Parameters, "includeAcr")
	assert.Equal(t, true, doc.Parameters["includeAcr"].Value)

	require.Contains(t, doc.Parameters, "deployments")
	deps, ok := doc.Parameters["deployments"].Value.([]any)
	require.True(t, ok, "deployments should be an array, got %T",
		doc.Parameters["deployments"].Value)
	require.Len(t, deps, 1)

	// Deploy-time-only params that we intentionally omit so the file isn't
	// stale the moment the user runs `azd env new`.
	for _, k := range []string{
		"location", "foundryProjectName", "resourceTokenSalt",
		"principalId", "tags",
	} {
		assert.NotContains(t, doc.Parameters, k,
			"%s is supplied at provision time and must not be hard-coded in the ejected file", k)
	}
}

func TestEjectInfra_HappyPath_NoDockerOmitsAcrParam(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	dir := t.TempDir()
	// No docker: block -> includeAcr should be false in the params file
	// but the acr.bicep module is still written (the template files are a
	// static set; whether ACR is provisioned is a parameter decision).
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.agent
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir))
	})

	// acr.bicep is still in the ejected tree -- the template is static.
	_, err := os.Stat(filepath.Join(dir, "infra", "modules", "acr.bicep"))
	assert.NoError(t, err, "acr.bicep module is part of the static template set")

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json"))
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, false, doc.Parameters["includeAcr"].Value)
}

func TestEjectInfra_RefusesWhenInfraIsAFile(t *testing.T) {
	t.Parallel()
	// Pre-existing `infra` as a regular file (not a directory) hits the
	// same "already exists" refusal as a pre-existing directory. os.Stat
	// can't tell the caller's intent apart, and overwriting a user file
	// silently would violate "no implicit destruction of user-owned files".
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	mustWriteFile(t, filepath.Join(dir, "infra"), "this is a file, not a dir")

	err := ejectInfra(dir)
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code,
		"a pre-existing file at ./infra is reported as an exists conflict, "+
			"not silently overwritten")

	// User's file must survive the refusal.
	got, err := os.ReadFile(filepath.Join(dir, "infra"))
	require.NoError(t, err)
	assert.Equal(t, "this is a file, not a dir", string(got))
}

func TestValidateStandaloneEjectArgs(t *testing.T) {
	// The standalone-eject branch in init.go runs after positional-arg
	// resolution, so by the time validateStandaloneEjectArgs is called
	// flags.manifestPointer / flags.src may have been set by
	// applyPositionalArg even if the user never passed a `-m` or `--src`.
	// Either way: any of args, manifestPointer, or src being set means
	// init-driving input that standalone eject cannot honor.
	tests := []struct {
		name      string
		args      []string
		manifest  string
		src       string
		wantError bool
	}{
		{name: "no extras: ok", args: nil, manifest: "", src: "", wantError: false},
		{name: "positional arg: refuse", args: []string{"./foo"}, wantError: true},
		{name: "manifest flag: refuse", manifest: "./agent.yaml", wantError: true},
		{name: "src flag: refuse", src: "./src/agent", wantError: true},
		{
			name:      "all three set: refuse",
			args:      []string{"./pos"},
			manifest:  "./agent.yaml",
			src:       "./src",
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flags := &initFlags{
				manifestPointer: tt.manifest,
				src:             tt.src,
			}
			err := validateStandaloneEjectArgs(tt.args, flags)
			if !tt.wantError {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected *azdext.LocalError, got %T", err)
			assert.Equal(t, exterrors.CodeInfraEjectConflictingArguments, localErr.Code)
			assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category,
				"the conflict is bad-user-input, classified Validation")
			// Suggestion must point at both ways out: drop the arg, or drop --infra.
			assert.Contains(t, localErr.Suggestion, "drop the extra argument")
			assert.Contains(t, localErr.Suggestion, "remove --infra")
		})
	}
}
