// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestPublishSkillMarkers(t *testing.T) {
	t.Parallel()

	values := map[string]string{}
	var writes []string
	err := publishSkillMarkers(
		t.Context(),
		"my--skill",
		"3",
		"https://example.test/projects/current",
		"test-env",
		func(_ context.Context, envName, key, value string) error {
			require.Equal(t, "test-env", envName)
			writes = append(writes, key+"="+value)
			values[key] = value
			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, "3", values["SKILL_MY__SKILL_VERSION"])
	require.Equal(
		t,
		"https://example.test/projects/current",
		values["SKILL_MY__SKILL_PROJECT_ENDPOINT"],
	)
	require.Equal(t, []string{
		"SKILL_MY__SKILL_VERSION=",
		"SKILL_MY__SKILL_PROJECT_ENDPOINT=https://example.test/projects/current",
		"SKILL_MY__SKILL_VERSION=3",
	}, writes)
}

func TestPublishSkillMarkersRejectsEmptyVersion(t *testing.T) {
	t.Parallel()

	called := false
	err := publishSkillMarkers(
		t.Context(),
		"skill",
		"",
		"https://example.test/projects/current",
		"test-env",
		func(context.Context, string, string, string) error {
			called = true
			return nil
		},
	)
	require.Error(t, err)
	require.False(t, called)
}

func TestParseSkillServiceConfig_ServiceLevel(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"description":   "code review skill",
		"instructions":  "Review code for correctness.",
		"license":       "MIT",
		"compatibility": "gpt-5",
		"metadata": map[string]any{
			"owner": "platform",
		},
		"tools": []any{"code_interpreter"},
	})
	require.NoError(t, err)

	cfg, err := parseSkillServiceConfig(&azdext.ServiceConfig{
		Name:                 "code-review",
		Host:                 aiSkillHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "code review skill", cfg.Description)
	assert.Equal(t, "Review code for correctness.", cfg.Instructions)
	assert.Equal(t, "MIT", cfg.License)
	assert.Equal(t, "gpt-5", cfg.Compatibility)
	assert.Equal(t, map[string]string{"owner": "platform"}, cfg.Metadata)
	assert.Equal(t, []string{"code_interpreter"}, cfg.Tools)
}

// TestParseSkillServiceConfig_ConfigFallback verifies skills written before the
// per-resource service split (config-nested shape) still parse.
func TestParseSkillServiceConfig_ConfigFallback(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"instructions": "legacy shape",
	})
	require.NoError(t, err)

	cfg, err := parseSkillServiceConfig(&azdext.ServiceConfig{
		Name:   "legacy",
		Host:   aiSkillHost,
		Config: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "legacy shape", cfg.Instructions)
}

func TestParseSkillServiceConfig_Empty(t *testing.T) {
	t.Parallel()

	cfg, err := parseSkillServiceConfig(&azdext.ServiceConfig{Name: "empty", Host: aiSkillHost})
	require.NoError(t, err)
	assert.Empty(t, cfg.Instructions)
}

func TestParseSkillServiceConfig_Archive(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"archive": "skills/code-review",
	})
	require.NoError(t, err)

	cfg, err := parseSkillServiceConfig(&azdext.ServiceConfig{
		Name:                 "code-review",
		Host:                 aiSkillHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "skills/code-review", cfg.Archive)
}

func TestValidateSkillServiceConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  skillServiceConfig
		wantErr string
	}{
		{
			name:   "inline",
			config: skillServiceConfig{Instructions: "Review code."},
		},
		{
			name:   "archive",
			config: skillServiceConfig{Archive: "skills/review"},
		},
		{
			name: "archive with inline fields",
			config: skillServiceConfig{
				Archive:      "skills/review",
				Instructions: "Review code.",
			},
			wantErr: "cannot combine archive",
		},
		{
			name:    "missing content",
			config:  skillServiceConfig{Description: "Review code"},
			wantErr: "requires instructions or archive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateSkillServiceConfig("review", &tt.config)
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestSkillInlineContent_PreservesSkillMdFields(t *testing.T) {
	t.Parallel()

	content := skillInlineContent(&skillServiceConfig{
		Description:   "Review code",
		License:       "MIT",
		Compatibility: "gpt-5",
		Metadata:      map[string]string{"owner": "platform"},
		Tools:         []string{"code_interpreter"},
	}, "Review for correctness.")

	assert.Equal(t, "Review code", content.Description)
	assert.Equal(t, "Review for correctness.", content.Instructions)
	assert.Equal(t, "MIT", content.License)
	assert.Equal(t, "gpt-5", content.Compatibility)
	assert.Equal(t, map[string]string{"owner": "platform"}, content.Metadata)
	assert.Equal(t, []string{"code_interpreter"}, content.AllowedTools)
}

func TestResolveSkillInstructions_Inline(t *testing.T) {
	t.Parallel()

	got, err := resolveSkillInstructions(
		"",
		&azdext.ServiceConfig{Name: "inline"},
		"Review code for correctness.",
	)
	require.NoError(t, err)
	assert.Equal(t, "Review code for correctness.", got)
}

func TestResolveSkillInstructions_MultilineBodyEndingInFileExtensionIsInline(t *testing.T) {
	t.Parallel()

	instructions := "# Rules\nSee CONTRIBUTING.md"
	got, err := resolveSkillInstructions("", &azdext.ServiceConfig{Name: "inline"}, instructions)
	require.NoError(t, err)
	assert.Equal(t, instructions, got)
}

func TestResolveSkillInstructions_FilePath(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	serviceDir := filepath.Join(projectDir, "skills", "review")
	require.NoError(t, os.MkdirAll(serviceDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(serviceDir, "instructions.md"),
		[]byte("Review from file."),
		0600,
	))

	got, err := resolveSkillInstructions(
		projectDir,
		&azdext.ServiceConfig{Name: "file", RelativePath: filepath.Join("skills", "review")},
		"instructions.md",
	)
	require.NoError(t, err)
	assert.Equal(t, "Review from file.", got)
}

func TestResolveSkillInstructions_EmptyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instructions.md"), []byte(" \n"), 0600))

	_, err := resolveSkillInstructions(
		"",
		&azdext.ServiceConfig{Name: "empty", RelativePath: dir},
		"instructions.md",
	)
	require.ErrorContains(t, err, "resolved to empty instructions")
}

// TestResolveSkillInstructions_PathTraversal verifies a relative instructions
// path that tries to escape the service directory with ".." is rejected rather
// than read from disk.
func TestResolveSkillInstructions_PathTraversal(t *testing.T) {
	t.Parallel()

	for _, instructions := range []string{"../secret.md", "../../etc/passwd.txt", "sub/../../escape.md"} {
		_, err := resolveSkillInstructions(
			"",
			&azdext.ServiceConfig{Name: "traversal", RelativePath: t.TempDir()},
			instructions,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not contain '..'")
	}
}

func TestResolveSkillArchivePath_PathTraversal(t *testing.T) {
	t.Parallel()

	for _, archive := range []string{"../skill.zip", "../../secret", "sub/../../escape.zip"} {
		_, err := resolveSkillArchivePath(
			t.TempDir(),
			&azdext.ServiceConfig{Name: "traversal", RelativePath: "skills"},
			archive,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not contain '..'")
	}
}

func TestResolveSkillPaths_FromNestedWorkingDirectory(t *testing.T) {
	projectDir := t.TempDir()
	serviceDir := filepath.Join(projectDir, "skills")
	nestedDir := filepath.Join(projectDir, "scripts", "nested")
	require.NoError(t, os.MkdirAll(serviceDir, 0750))
	require.NoError(t, os.MkdirAll(nestedDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(serviceDir, "instructions.md"), []byte("Review."), 0600))
	t.Chdir(nestedDir)

	service := &azdext.ServiceConfig{Name: "code-review", RelativePath: "skills"}
	instructions, err := resolveSkillInstructions(projectDir, service, "instructions.md")
	require.NoError(t, err)
	assert.Equal(t, "Review.", instructions)

	archive, err := resolveSkillArchivePath(projectDir, service, "code-review")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(projectDir, "skills", "code-review"), archive)
}

func TestPrepareSkillArchive_Directory(t *testing.T) {
	t.Parallel()

	dir := writeSkillDir(t, "code-review")
	archive, err := prepareSkillArchive(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = archive.Reader.Close() })

	assert.Equal(t, filepath.Base(dir)+".zip", archive.Name)
	data, err := io.ReadAll(archive.Reader)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestPrepareSkillArchive_Zip(t *testing.T) {
	t.Parallel()

	path := writeZipWithSkillMd(t, "code-review")
	archive, err := prepareSkillArchive(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = archive.Reader.Close() })

	assert.Equal(t, filepath.Base(path), archive.Name)
	data, err := io.ReadAll(archive.Reader)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestPrepareSkillArchive_RejectsDirectoryWithoutSkillMd(t *testing.T) {
	t.Parallel()

	archive, err := prepareSkillArchive(t.TempDir())
	require.ErrorContains(t, err, "does not contain SKILL.md")
	assert.Nil(t, archive)
}

func TestPrepareSkillArchive_RejectsUnsupportedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte("instructions"), 0600))

	archive, err := prepareSkillArchive(path)
	require.Error(t, err)
	assert.Nil(t, archive)
	assert.Contains(t, err.Error(), ".zip")
}

func TestPrepareSkillArchive_RejectsNonRegularZip(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link requires elevated privileges on Windows")
	}

	path := filepath.Join(t.TempDir(), "skill.zip")
	require.NoError(t, os.Symlink(os.DevNull, path))

	archive, err := prepareSkillArchive(path)
	require.ErrorContains(t, err, "must be a regular .zip file")
	assert.Nil(t, archive)
}
