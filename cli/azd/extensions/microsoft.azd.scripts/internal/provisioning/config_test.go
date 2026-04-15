// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseProviderConfig_ValidMultiScript(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "scripts/setup.sh", "#!/bin/bash\necho hello")
	createFile(t, dir, "scripts/teardown.sh", "#!/bin/bash\necho bye")

	raw := map[string]any{
		"provision": []any{
			map[string]any{
				"kind": "sh",
				"run":  "scripts/setup.sh",
				"name": "Setup",
				"env": map[string]any{
					"FOO": "bar",
				},
			},
		},
		"destroy": []any{
			map[string]any{
				"kind": "sh",
				"run":  "scripts/teardown.sh",
			},
		},
	}

	cfg, err := ParseProviderConfig(raw)
	require.NoError(t, err)
	require.Len(t, cfg.Provision, 1)
	require.Len(t, cfg.Destroy, 1)
	require.Equal(t, "sh", cfg.Provision[0].Kind)
	require.Equal(t, "Setup", cfg.Provision[0].Name)
	require.Equal(t, "bar", cfg.Provision[0].Env["FOO"])

	err = cfg.Validate(dir)
	require.NoError(t, err)
}

func TestParseProviderConfig_EmptyConfig(t *testing.T) {
	cfg, err := ParseProviderConfig(nil)
	require.NoError(t, err)

	err = cfg.Validate(t.TempDir())
	require.ErrorContains(t, err, "at least one")
}

func TestValidateScriptConfig_MissingRun(t *testing.T) {
	dir := t.TempDir()

	cfg := &ProviderConfig{
		Provision: []*ScriptConfig{{Kind: "sh", Run: ""}},
	}
	err := cfg.Validate(dir)
	require.ErrorContains(t, err, "'run' is required")
}

func TestValidateScriptConfig_AbsolutePath(t *testing.T) {
	dir := t.TempDir()

	cfg := &ProviderConfig{
		Provision: []*ScriptConfig{{Kind: "sh", Run: filepath.Join(dir, "path.sh")}},
	}
	err := cfg.Validate(dir)
	require.ErrorContains(t, err, "must be relative")
}

func TestValidateScriptConfig_PathTraversal(t *testing.T) {
	dir := t.TempDir()

	cfg := &ProviderConfig{
		Provision: []*ScriptConfig{{Kind: "sh", Run: "../../../etc/passwd"}},
	}
	err := cfg.Validate(dir)
	require.ErrorContains(t, err, "path traversal")
}

func TestValidateScriptConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()

	cfg := &ProviderConfig{
		Provision: []*ScriptConfig{{Kind: "sh", Run: "nonexistent.sh"}},
	}
	err := cfg.Validate(dir)
	require.ErrorContains(t, err, "Script file not found")
}

func TestValidateScriptConfig_UnknownKind(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "script.sh", "#!/bin/bash")

	cfg := &ProviderConfig{
		Provision: []*ScriptConfig{{Kind: "python", Run: "script.sh"}},
	}
	err := cfg.Validate(dir)
	require.ErrorContains(t, err, "unsupported kind")
}

func TestNormalizeKind_InferFromExtension(t *testing.T) {
	tests := []struct {
		run      string
		expected string
	}{
		{"scripts/setup.sh", "sh"},
		{"scripts/setup.ps1", "pwsh"},
		{"scripts/setup.txt", ""},
	}

	for _, tt := range tests {
		sc := &ScriptConfig{Run: tt.run}
		normalizeKind(sc)
		require.Equal(t, tt.expected, sc.Kind, "for %s", tt.run)
	}
}

func TestNormalizeKind_ShellToKindBackcompat(t *testing.T) {
	sc := &ScriptConfig{Run: "setup.sh", Shell: "sh"}
	normalizeKind(sc)
	require.Equal(t, "sh", sc.Kind)
	require.Equal(t, "", sc.Shell, "Shell should be cleared after mapping to Kind")
}

func TestNormalizeKind_KindTakesPrecedence(t *testing.T) {
	sc := &ScriptConfig{Run: "setup.sh", Kind: "pwsh", Shell: "sh"}
	normalizeKind(sc)
	require.Equal(t, "pwsh", sc.Kind)
}

func TestParseProviderConfig_DefaultNames(t *testing.T) {
	raw := map[string]any{
		"provision": []any{
			map[string]any{"kind": "sh", "run": "a.sh"},
			map[string]any{"kind": "sh", "run": "b.sh"},
		},
	}

	cfg, err := ParseProviderConfig(raw)
	require.NoError(t, err)
	require.Equal(t, "provision[0]", cfg.Provision[0].Name)
	require.Equal(t, "provision[1]", cfg.Provision[1].Name)
}

func TestValidateScriptConfig_CantDetermineKind(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "script.txt", "hello")

	cfg := &ProviderConfig{
		Provision: []*ScriptConfig{{Run: "script.txt"}},
	}
	err := cfg.Validate(dir)
	require.ErrorContains(t, err, "unable to determine script type")
}

func TestValidateScriptConfig_ContinueOnError(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "setup.sh", "#!/bin/bash")

	cfg := &ProviderConfig{
		Provision: []*ScriptConfig{{
			Kind:            "sh",
			Run:             "setup.sh",
			ContinueOnError: true,
		}},
	}
	err := cfg.Validate(dir)
	require.NoError(t, err)
	require.True(t, cfg.Provision[0].ContinueOnError)
}

func TestApplyPlatformOverride_DoesNotResetContinueOnError(t *testing.T) {
	sc := &ScriptConfig{
		Kind:            "sh",
		Run:             "fallback.sh",
		ContinueOnError: true,
		Posix: &ScriptConfig{
			Run: "linux-specific.sh",
		},
		Windows: &ScriptConfig{
			Run: "windows-specific.sh",
		},
	}

	applyPlatformOverride(sc)

	// ContinueOnError should NOT be reset to false by a platform override
	// that doesn't explicitly set it.
	require.True(t, sc.ContinueOnError, "ContinueOnError should not be reset by platform override")
}

func TestApplyPlatformOverride_MergesEnvMaps(t *testing.T) {
	sc := &ScriptConfig{
		Kind: "sh",
		Run:  "base.sh",
		Env:  map[string]string{"A": "base", "B": "base"},
		Posix: &ScriptConfig{
			Env: map[string]string{"B": "override", "C": "new"},
		},
		Windows: &ScriptConfig{
			Env: map[string]string{"B": "win-override", "D": "win-new"},
		},
	}

	applyPlatformOverride(sc)

	require.Equal(t, "base", sc.Env["A"], "base env preserved")
	// Depending on platform, B should be overridden
	require.NotEmpty(t, sc.Env["B"])
}

func createFile(t *testing.T, base, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(base, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o600))
}
