// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnownTargets_MapsToExpectedPaths(t *testing.T) {
	// Pin the target -> install path table. Any change here is a wire
	// contract change consumed by azure.ai.agents init's interactive
	// pre-flow and by automation that pre-creates the directory.
	cases := []struct {
		target  string
		wantDir string // empty for "custom" (uses --path)
	}{
		{"claude", filepath.Join(".claude", "skills", "azd-ai-skill")},
		{"codex", filepath.Join(".agents", "skills", "azd-ai-skill")},
		{"gemini", filepath.Join(".agents", "skills", "azd-ai-skill")},
		{"copilot", filepath.Join(".agents", "skills", "azd-ai-skill")},
		{"opencode", filepath.Join(".agents", "skills", "azd-ai-skill")},
		{"custom", ""},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			got, ok := lookupTarget(tc.target)
			require.True(t, ok, "target %q should be known", tc.target)
			assert.Equal(t, tc.wantDir, got.installDir)
		})
	}
}

func TestKnownTargets_IncludesAllExpectedNames(t *testing.T) {
	// Drift guard: if a target is added or removed, the agents extension
	// pre-flow's Select choices must be updated in lockstep.
	want := []string{"claude", "codex", "gemini", "copilot", "opencode", "custom"}
	assert.Equal(t, want, targetNames())
}

func TestLookupTarget_IsCaseInsensitive(t *testing.T) {
	got, ok := lookupTarget("Copilot")
	require.True(t, ok)
	assert.Equal(t, "copilot", got.name)
}

func TestValidateSkillInstallFlags_RequiresTarget(t *testing.T) {
	err := validateSkillInstallFlags(&skillInstallFlags{}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--target is required")
}

func TestValidateSkillInstallFlags_RejectsUnknownTarget(t *testing.T) {
	err := validateSkillInstallFlags(&skillInstallFlags{target: "anthropic"}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown --target")
}

func TestValidateSkillInstallFlags_CustomRequiresPath(t *testing.T) {
	err := validateSkillInstallFlags(&skillInstallFlags{target: "custom"}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--path is required when --target=custom")
}

func TestValidateSkillInstallFlags_RejectsPathWithNonCustomTarget(t *testing.T) {
	err := validateSkillInstallFlags(&skillInstallFlags{
		target: "copilot",
		path:   ".my-tool/skills",
	}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--path is only valid with --target=custom")
}

func TestValidateSkillInstallFlags_AcceptsValidCustomPath(t *testing.T) {
	err := validateSkillInstallFlags(&skillInstallFlags{
		target: "custom",
		path:   ".my-tool/skills/foundry",
	}, t.TempDir())
	require.NoError(t, err)
}

func TestValidateCustomPath_Safety(t *testing.T) {
	cwd := t.TempDir()
	cases := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"empty", "", "must not be empty"},
		{"dot", ".", "not a valid"},
		{"dotdot", "..", "not a valid"},
		{"absolute_unix", "/etc/foo", "must be relative"},
		{"escape", "../outside", "escapes the project root"},
		{"deep_escape", "valid/../../escape", "escapes the project root"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCustomPath(tc.path, cwd)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestValidateCustomPath_RejectsWindowsAbsolutePath(t *testing.T) {
	// On Windows filepath.IsAbs catches "C:\..."; the explicit leading-
	// separator check below covers forward-slash "absolute" paths on
	// every OS so a malicious value cannot slip through on POSIX.
	cwd := t.TempDir()
	err := validateCustomPath(`\foo\bar`, cwd)
	require.Error(t, err)
	if runtime.GOOS == "windows" {
		assert.Contains(t, err.Error(), "must be relative")
	} else {
		assert.Contains(t, err.Error(), "must be relative")
	}
}

func TestValidateCustomPath_DetectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation on Windows requires Developer Mode; covered by POSIX runners")
	}
	cwd := t.TempDir()
	outside := t.TempDir()
	// Create cwd/escape -> outside (a real symlink).
	link := filepath.Join(cwd, "escape")
	require.NoError(t, os.Symlink(outside, link))

	err := validateCustomPath("escape/skills/foundry", cwd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
}

func TestSkillInstallAction_InstallsPackToTarget(t *testing.T) {
	cwd := t.TempDir()
	pack := newTestPack(map[string]string{
		"SKILL.md":         "skill body",
		"helpers/extra.md": "extra body",
	})

	action := &SkillInstallAction{
		flags: &skillInstallFlags{
			target: "copilot",
			output: "text",
		},
		out:      &bytes.Buffer{},
		cwd:      cwd,
		packs:    pack,
		packName: defaultPackName,
	}
	require.NoError(t, action.Run(context.Background()))

	dest := filepath.Join(cwd, ".agents", "skills", "azd-ai-skill")
	assertFileContent(t, filepath.Join(dest, "SKILL.md"), "skill body")
	assertFileContent(t, filepath.Join(dest, "helpers", "extra.md"), "extra body")
}

func TestSkillInstallAction_CustomPathWritesToProvidedDir(t *testing.T) {
	cwd := t.TempDir()
	pack := newTestPack(map[string]string{"SKILL.md": "x"})

	action := &SkillInstallAction{
		flags: &skillInstallFlags{
			target: "custom",
			path:   ".my-tool/skills/foundry",
			output: "text",
		},
		out:      &bytes.Buffer{},
		cwd:      cwd,
		packs:    pack,
		packName: defaultPackName,
	}
	require.NoError(t, action.Run(context.Background()))
	assertFileContent(t, filepath.Join(cwd, ".my-tool", "skills", "foundry", "SKILL.md"), "x")
}

func TestSkillInstallAction_RefusesToOverwriteModifiedOwnedFile(t *testing.T) {
	cwd := t.TempDir()
	pack := newTestPack(map[string]string{"SKILL.md": "bundled body"})
	dest := filepath.Join(cwd, ".agents", "skills", "azd-ai-skill")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("user edited"), 0o644))

	action := &SkillInstallAction{
		flags:    &skillInstallFlags{target: "copilot", output: "text"},
		out:      &bytes.Buffer{},
		cwd:      cwd,
		packs:    pack,
		packName: defaultPackName,
	}
	err := action.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to overwrite")
	// The user edit must still be on disk (no destructive write before
	// the conflict check).
	assertFileContent(t, filepath.Join(dest, "SKILL.md"), "user edited")
}

func TestSkillInstallAction_ForceOverwritesModifiedOwnedFile(t *testing.T) {
	cwd := t.TempDir()
	pack := newTestPack(map[string]string{"SKILL.md": "bundled body"})
	dest := filepath.Join(cwd, ".agents", "skills", "azd-ai-skill")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("user edited"), 0o644))

	action := &SkillInstallAction{
		flags:    &skillInstallFlags{target: "copilot", force: true, output: "text"},
		out:      &bytes.Buffer{},
		cwd:      cwd,
		packs:    pack,
		packName: defaultPackName,
	}
	require.NoError(t, action.Run(context.Background()))
	assertFileContent(t, filepath.Join(dest, "SKILL.md"), "bundled body")
}

func TestSkillInstallAction_LeavesForeignFilesUntouched(t *testing.T) {
	cwd := t.TempDir()
	pack := newTestPack(map[string]string{"SKILL.md": "owned"})
	dest := filepath.Join(cwd, ".agents", "skills", "azd-ai-skill")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	// Foreign file -- not in the pack manifest. Must survive both
	// initial install and --force re-install.
	require.NoError(t, os.WriteFile(filepath.Join(dest, "notes.md"), []byte("user notes"), 0o644))

	action := &SkillInstallAction{
		flags:    &skillInstallFlags{target: "copilot", force: true, output: "text"},
		out:      &bytes.Buffer{},
		cwd:      cwd,
		packs:    pack,
		packName: defaultPackName,
	}
	require.NoError(t, action.Run(context.Background()))
	assertFileContent(t, filepath.Join(dest, "SKILL.md"), "owned")
	assertFileContent(t, filepath.Join(dest, "notes.md"), "user notes")
}

func TestSkillInstallAction_IdempotentWhenContentMatches(t *testing.T) {
	cwd := t.TempDir()
	pack := newTestPack(map[string]string{"SKILL.md": "same"})
	dest := filepath.Join(cwd, ".agents", "skills", "azd-ai-skill")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("same"), 0o644))

	// Re-install with the same content; no conflict, no --force needed.
	action := &SkillInstallAction{
		flags:    &skillInstallFlags{target: "copilot", output: "text"},
		out:      &bytes.Buffer{},
		cwd:      cwd,
		packs:    pack,
		packName: defaultPackName,
	}
	require.NoError(t, action.Run(context.Background()))
	assertFileContent(t, filepath.Join(dest, "SKILL.md"), "same")
}

func TestSkillInstallAction_JSONOutputShape(t *testing.T) {
	cwd := t.TempDir()
	pack := newTestPack(map[string]string{
		"SKILL.md":         "x",
		"helpers/extra.md": "y",
	})

	var buf bytes.Buffer
	action := &SkillInstallAction{
		flags:    &skillInstallFlags{target: "copilot", output: "json"},
		out:      &buf,
		cwd:      cwd,
		packs:    pack,
		packName: defaultPackName,
	}
	require.NoError(t, action.Run(context.Background()))

	var got skillInstallResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "installed", got.Status)
	assert.Equal(t, "copilot", got.Target)
	assert.Equal(t, ".agents/skills/azd-ai-skill", got.Path)
	assert.Equal(t, []string{"SKILL.md", "helpers/extra.md"}, got.Files)
}

// TestEmbeddedPackHasSKILLMd is a smoke test that the build-time embed
// directive actually pulled in our skill files. If this fails,
// //go:embed in skill_install.go is broken or the directory layout
// drifted.
func TestEmbeddedPackHasSKILLMd(t *testing.T) {
	files, err := readPack(skillFilesFS)
	require.NoError(t, err)
	require.NotEmpty(t, files)

	var hasSkill bool
	for _, f := range files {
		if f.relPath == "SKILL.md" {
			hasSkill = true
			break
		}
	}
	assert.True(t, hasSkill, "embedded skills should include SKILL.md, got: %v", relPaths(files))
}

// newTestPack returns an fs.FS shaped like the real embedded layout
// (skills/<files>) so tests can drive Run() without rebuilding the
// embed.
func newTestPack(files map[string]string) fs.FS {
	mfs := fstest.MapFS{}
	for rel, body := range files {
		mfs["skills/"+rel] = &fstest.MapFile{Data: []byte(body)}
	}
	return mfs
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	require.NoError(t, err, "file %s should exist", path)
	assert.Equal(t, want, string(got))
}

func relPaths(files []packFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.relPath)
	}
	return out
}

// TestNewInstallSkillCommand_DoesNotRegisterReservedOutputFlag pins the
// reserved-flag contract: --output MUST be registered via the SDK helper,
// not via cmd.Flags().StringVar. The azd host rejects extensions that
// define their own --output cobra flag (see PR #b28ae56fd).
func TestNewInstallSkillCommand_DoesNotRegisterReservedOutputFlag(t *testing.T) {
	cmd := newInstallSkillCommand(nil)
	// If the install command ever falls back to cmd.Flags().StringVar
	// for --output, the flag will be visible on the cobra FlagSet AND
	// the azd host startup will reject the extension. The SDK helper
	// path does NOT register --output via cmd.Flags() -- it lives in a
	// side table the host reads after registration.
	assert.Nil(t, cmd.Flags().Lookup("output"),
		"command must not register reserved flag --output via cmd.Flags().StringVar")

	// Sanity: declared flags exist.
	require.NotNil(t, cmd.Flags().Lookup("target"))
	require.NotNil(t, cmd.Flags().Lookup("path"))
	require.NotNil(t, cmd.Flags().Lookup("force"))
}
