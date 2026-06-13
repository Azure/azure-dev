// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubCompiler is a hand-rolled bicepCompiler that records calls and
// returns canned results. Tests use this instead of mocking the
// full *bicep.Cli (which requires console + commandRunner + actual
// bicep binary).
type stubCompiler struct {
	buildCalls      []string // files passed to Build
	buildParamCalls []string // files passed to BuildBicepParam
	buildParamEnvs  [][]string

	buildResult      bicep.BuildResult
	buildErr         error
	buildParamResult bicep.BuildResult
	buildParamErr    error
}

func (s *stubCompiler) Build(ctx context.Context, file string) (bicep.BuildResult, error) {
	s.buildCalls = append(s.buildCalls, file)
	return s.buildResult, s.buildErr
}

func (s *stubCompiler) BuildBicepParam(ctx context.Context, file string, env []string) (bicep.BuildResult, error) {
	s.buildParamCalls = append(s.buildParamCalls, file)
	// Sort the env so test assertions are deterministic; map iteration
	// in envValuesToKeyEquals is random.
	envCopy := append([]string(nil), env...)
	sort.Strings(envCopy)
	s.buildParamEnvs = append(s.buildParamEnvs, envCopy)
	return s.buildParamResult, s.buildParamErr
}

// minimalARMTemplate returns a JSON string for the smallest valid ARM
// template; tests use it as the canned bicep Build output.
func minimalARMTemplate() string {
	return `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "contentVersion": "1.0.0.0",
  "resources": []
}`
}

// minimalARMParametersFile returns a JSON parameters file with the
// supplied (name, value) entries already in the {"value": ...}
// envelope shape.
func minimalARMParametersFile(t *testing.T, entries map[string]any) string {
	t.Helper()
	wrapped := map[string]map[string]any{}
	for k, v := range entries {
		wrapped[k] = map[string]any{"value": v}
	}
	doc := map[string]any{
		"$schema": "https://schema.management.azure.com/" +
			"schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters":     wrapped,
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	return string(out)
}

func TestLoadOnDiskTemplate_NoInfra(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stub := &stubCompiler{}

	got, err := loadOnDiskTemplate(t.Context(), dir, stub, nil)
	require.NoError(t, err, "absent ./infra/ must NOT be an error")
	assert.Nil(t, got, "absent ./infra/ must return nil source so caller falls back to embedded")
	assert.Empty(t, stub.buildCalls, "Build must not be called when no on-disk template exists")
	assert.Empty(t, stub.buildParamCalls, "BuildBicepParam must not be called either")
}

func TestLoadOnDiskTemplate_BicepOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	infraDir := filepath.Join(dir, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	bicepPath := filepath.Join(infraDir, onDiskBicepFile)
	require.NoError(t, os.WriteFile(bicepPath, []byte("// fake bicep\n"), 0o600))

	stub := &stubCompiler{buildResult: bicep.BuildResult{Compiled: minimalARMTemplate()}}

	got, err := loadOnDiskTemplate(t.Context(), dir, stub, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, templateModeBicep, got.mode)
	assert.Equal(t, bicepPath, got.sourcePath)
	// armTemplate must be a parsed JSON object, not the raw string.
	assert.Equal(t, "1.0.0.0", got.armTemplate["contentVersion"])
	// No parameters file present -> empty parameter map (NOT nil so
	// merge logic can range over it safely).
	assert.NotNil(t, got.parameters)
	assert.Empty(t, got.parameters)

	require.Len(t, stub.buildCalls, 1, "Build called exactly once for the bicep path")
	assert.Equal(t, bicepPath, stub.buildCalls[0])
	assert.Empty(t, stub.buildParamCalls, "BuildBicepParam must NOT be called when no .bicepparam present")
}

func TestLoadOnDiskTemplate_BicepWithParams(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	infraDir := filepath.Join(dir, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// bicep\n"), 0o600))

	params := minimalARMParametersFile(t, map[string]any{
		"location":           "${AZURE_LOCATION}", // ${VAR} resolved
		"foundryProjectName": "my-project",        // literal
		"shouldDrop":         "${UNSET_VAR}",      // unresolved -> dropped
	})
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskParamsFile), []byte(params), 0o600))

	stub := &stubCompiler{buildResult: bicep.BuildResult{Compiled: minimalARMTemplate()}}
	envValues := map[string]string{"AZURE_LOCATION": "eastus"}

	got, err := loadOnDiskTemplate(t.Context(), dir, stub, envValues)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, templateModeBicep, got.mode)

	// Resolved VAR -> kept with the substituted value.
	require.Contains(t, got.parameters, "location")
	locEntry, ok := got.parameters["location"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "eastus", locEntry["value"])

	// Literal -> kept verbatim.
	require.Contains(t, got.parameters, "foundryProjectName")

	// Unresolved VAR -> dropped so the template's default (if any)
	// wins, matching core's behavior at bicep_provider.go:2259-2262.
	assert.NotContains(t, got.parameters, "shouldDrop",
		"parameters referencing an unresolved ${VAR} must be dropped, not set to empty string")
}

func TestLoadOnDiskTemplate_BicepparamPrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	infraDir := filepath.Join(dir, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	// Both .bicep and .bicepparam present; .bicepparam must win.
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// bicep"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepParamFile), []byte("// bicepparam"), 0o600))

	envelope, err := json.Marshal(map[string]string{
		"templateJson": minimalARMTemplate(),
		"parametersJson": minimalARMParametersFile(t, map[string]any{
			"foo": "bar",
		}),
	})
	require.NoError(t, err)

	stub := &stubCompiler{buildParamResult: bicep.BuildResult{Compiled: string(envelope)}}

	got, err := loadOnDiskTemplate(t.Context(), dir, stub, map[string]string{"K": "v"})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, templateModeBicepParam, got.mode)
	assert.Equal(t, filepath.Join(infraDir, onDiskBicepParamFile), got.sourcePath)

	// .bicepparam path is taken exclusively.
	require.Len(t, stub.buildParamCalls, 1)
	assert.Empty(t, stub.buildCalls, "Build must NOT be called when .bicepparam wins")

	// Envelope's parameters are extracted into the templateSource.
	require.Contains(t, got.parameters, "foo")

	// Env values are passed through so bicepparam's
	// readEnvironmentVariable() can resolve them.
	require.Len(t, stub.buildParamEnvs, 1)
	assert.Contains(t, stub.buildParamEnvs[0], "K=v")
}

func TestLoadOnDiskTemplate_CompileError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	infraDir := filepath.Join(dir, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// broken bicep"), 0o600))

	stub := &stubCompiler{buildErr: errors.New("bicep error BCP068: expected resource type string at line 3")}

	got, err := loadOnDiskTemplate(t.Context(), dir, stub, nil)
	require.Error(t, err)
	assert.Nil(t, got)

	var local *azdext.LocalError
	require.True(t, errors.As(err, &local), "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeOnDiskBicepCompileFailed, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category,
		"compile failures are Validation (user's bicep is wrong)")
	// The bicep CLI's own error message should be embedded so the
	// user can see what went wrong.
	assert.Contains(t, local.Message, "BCP068")
}

func TestLoadOnDiskTemplate_InvalidJSONFromBicep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	infraDir := filepath.Join(dir, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// bicep"), 0o600))

	// Bicep "succeeds" but returns junk (defensive case; shouldn't
	// happen in practice, but we surface it clearly).
	stub := &stubCompiler{buildResult: bicep.BuildResult{Compiled: "not actually JSON"}}

	got, err := loadOnDiskTemplate(t.Context(), dir, stub, nil)
	require.Error(t, err)
	assert.Nil(t, got)

	var local *azdext.LocalError
	require.True(t, errors.As(err, &local))
	assert.Equal(t, exterrors.CodeOnDiskBicepParseFailed, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryInternal, local.Category,
		"unparseable bicep output is Internal (bicep CLI bug, not user error)")
}

func TestLoadParametersFile_MissingFile(t *testing.T) {
	t.Parallel()
	got, err := loadParametersFile(filepath.Join(t.TempDir(), "does-not-exist.json"), nil)
	require.NoError(t, err, "missing parameters file must NOT be an error")
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestLoadParametersFile_VarResolution(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		fileBody   string
		envValues  map[string]string
		wantKey    string
		wantValue  any
		wantAbsent bool
	}{
		{
			name: "literal value passes through",
			fileBody: minimalARMParametersFile(t, map[string]any{
				"name": "literal-value",
			}),
			envValues: nil,
			wantKey:   "name",
			wantValue: "literal-value",
		},
		{
			name: "${VAR} present in env -> substituted",
			fileBody: minimalARMParametersFile(t, map[string]any{
				"location": "${AZURE_LOCATION}",
			}),
			envValues: map[string]string{"AZURE_LOCATION": "westus2"},
			wantKey:   "location",
			wantValue: "westus2",
		},
		{
			name: "${VAR} unset -> key dropped",
			fileBody: minimalARMParametersFile(t, map[string]any{
				"name": "${UNSET_VAR}",
			}),
			envValues:  map[string]string{},
			wantKey:    "name",
			wantAbsent: true,
		},
		{
			name: "${VAR=default} unset -> envsubst supplies the default",
			fileBody: minimalARMParametersFile(t, map[string]any{
				"name": "${UNSET_VAR=fallback}",
			}),
			envValues: nil,
			wantKey:   "name",
			wantValue: "fallback",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "main.parameters.json")
			require.NoError(t, os.WriteFile(path, []byte(tt.fileBody), 0o600))

			got, err := loadParametersFile(path, tt.envValues)
			require.NoError(t, err)

			if tt.wantAbsent {
				assert.NotContains(t, got, tt.wantKey,
					"unresolved ${VAR} must drop the parameter so the template default wins")
				return
			}

			require.Contains(t, got, tt.wantKey, "parameter %q must be present", tt.wantKey)
			entry, ok := got[tt.wantKey].(map[string]any)
			require.True(t, ok, "entry must keep the {value: ...} envelope")
			assert.Equal(t, tt.wantValue, entry["value"])
		})
	}
}

func TestLoadParametersFile_MalformedJSON(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.parameters.json")
	require.NoError(t, os.WriteFile(path, []byte("this is not JSON"), 0o600))

	_, err := loadParametersFile(path, nil)
	require.Error(t, err)
	var local *azdext.LocalError
	require.True(t, errors.As(err, &local))
	assert.Equal(t, exterrors.CodeOnDiskParametersInvalid, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category)
}

func TestMergeParameters_UserWins(t *testing.T) {
	t.Parallel()
	host := map[string]any{
		"location":    map[string]any{"value": "host-location"},
		"principalId": map[string]any{"value": "host-pid"},
	}
	user := map[string]any{
		"location":   map[string]any{"value": "user-location"},
		"includeAcr": map[string]any{"value": true},
	}
	got := mergeParameters(user, host)

	// Same key in both: user wins.
	loc := got["location"].(map[string]any)
	assert.Equal(t, "user-location", loc["value"])
	// Key only in user: present in result.
	require.Contains(t, got, "includeAcr")
	// Key only in host: present in result (user's file may not declare
	// every parameter the embedded host map covers).
	require.Contains(t, got, "principalId")
	pid := got["principalId"].(map[string]any)
	assert.Equal(t, "host-pid", pid["value"])
}

func TestMergeParameters_NilInputsAreSafe(t *testing.T) {
	t.Parallel()
	// All nil -> empty map, no panic.
	assert.NotNil(t, mergeParameters(nil, nil))
	assert.Empty(t, mergeParameters(nil, nil))
	// Nil user, non-nil host -> host passes through.
	got := mergeParameters(nil, map[string]any{"k": "v"})
	assert.Equal(t, "v", got["k"])
}

func TestTemplateMode_String(t *testing.T) {
	t.Parallel()
	// Strings end up in deployment tags and telemetry; lock the
	// values so future renames are deliberate.
	assert.Equal(t, "embedded", templateModeEmbedded.String())
	assert.Equal(t, "ondisk_bicep", templateModeBicep.String())
	assert.Equal(t, "ondisk_bicepparam", templateModeBicepParam.String())
}

// Behavior contract: nested ${VAR} references inside object/array
// values do NOT cause the parameter to be dropped. Only top-level
// STRING values that collapse to "" due to an unresolved ${VAR} are
// dropped. This matches core's bicep_provider.go:2335-2340, which
// only checks `stringValue == "" && hasUnsetEnvVar` against a string-
// typed `resolvedParam.Value`. Object/array entries are kept and ARM
// receives whatever the substitution produced (including empty
// strings buried inside the structure -- the user is responsible
// for handling those, or supplying the env var).
func TestLoadParametersFile_NestedUnresolvedIsKept(t *testing.T) {
	t.Parallel()
	body := `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "nested": {
      "value": {
        "inner": "${UNSET_VAR}"
      }
    },
    "topLevelDropped": {
      "value": "${UNSET_VAR}"
    },
    "ok": {
      "value": "literal"
    }
  }
}`
	path := filepath.Join(t.TempDir(), "main.parameters.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	got, err := loadParametersFile(path, nil)
	require.NoError(t, err)
	// Top-level string that collapses to "" is dropped (matches core).
	assert.NotContains(t, got, "topLevelDropped",
		"top-level string parameter that resolves to \"\" must be dropped")
	// Nested object reference is KEPT; user owns the consequences.
	require.Contains(t, got, "nested",
		"object-valued parameter is kept even when an inner string is unresolved (matches core)")
	// Literal is unaffected.
	require.Contains(t, got, "ok")
}

// Sanity test that the helper handles the path the production code
// actually exercises: writing the file via writeParametersFile in
// init_infra.go produces output that loadParametersFile can read back.
func TestLoadParametersFile_RoundTripWithEjectWriter(t *testing.T) {
	t.Parallel()
	// Simulate the eject writer's output: a parameters file with the
	// synthesizer's two derived values.
	body := minimalARMParametersFile(t, map[string]any{
		"deployments": []any{
			map[string]any{"name": "gpt-4-1-mini"},
		},
		"includeAcr": true,
	})
	path := filepath.Join(t.TempDir(), "main.parameters.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	got, err := loadParametersFile(path, nil)
	require.NoError(t, err)
	require.Contains(t, got, "deployments")
	require.Contains(t, got, "includeAcr")

	acr := got["includeAcr"].(map[string]any)
	assert.Equal(t, true, acr["value"])
	dep := got["deployments"].(map[string]any)
	depList, ok := dep["value"].([]any)
	require.True(t, ok, "deployments value must round-trip as an array")
	require.Len(t, depList, 1)
}

// fileExistsAt is a small enough helper to test directly; covers the
// "path is a directory" branch which the production code relies on.
func TestFileExistsAt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "x.txt")
	require.NoError(t, os.WriteFile(file, []byte("ok"), 0o600))

	assert.True(t, fileExistsAt(file), "regular file -> true")
	assert.False(t, fileExistsAt(dir), "directory -> false")
	assert.False(t, fileExistsAt(filepath.Join(dir, "missing")), "missing -> false")
}

// Defensive: confirm envValuesToKeyEquals produces the format the
// bicep CLI expects ("KEY=VALUE" strings). Order is undefined; sort
// for stability.
func TestEnvValuesToKeyEquals(t *testing.T) {
	t.Parallel()
	got := envValuesToKeyEquals(map[string]string{
		"A": "1",
		"B": "two=with=equals",
		"":  "empty-key-allowed",
	})
	sort.Strings(got)
	want := []string{"=empty-key-allowed", "A=1", "B=two=with=equals"}
	sort.Strings(want)
	assert.Equal(t, want, got)
}

// Ensure the bicepCompiler interface is satisfied by *bicep.Cli at
// compile time. The actual Cli requires console + commandRunner so we
// don't construct it here; just assert the interface contract.
var _ bicepCompiler = (*bicep.Cli)(nil)

// stubCompiler implements bicepCompiler; this guards against signature
// drift in the interface vs. the stub.
var _ bicepCompiler = (*stubCompiler)(nil)
