// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sameFileConversationRows = "{\"messages\":[{\"role\":\"user\",\"content\":\"Hi\"}," +
	"{\"role\":\"assistant\",\"content\":\"Hello\"}]}\r\n"

func TestInitRefusesDatasetAsConfigDestinationBeforeWrites(t *testing.T) {
	for _, form := range []string{"relative input", "absolute input", "normalized dots", "Windows case", "hard link", "symlink"} {
		t.Run(form, func(t *testing.T) {
			if form == "Windows case" && runtime.GOOS != "windows" {
				t.Skip("case-insensitive identity is Windows-specific")
			}
			h := newInitHarness(t, nil)
			dir := filepath.Join(h.dir, "shared files")
			require.NoError(t, os.MkdirAll(dir, 0o700))
			source := filepath.Join(dir, "shared391.yml")
			require.NoError(t, os.WriteFile(source, []byte(sameFileConversationRows), 0o600))
			dataset, destination := filepath.Join("shared files", "shared391.yml"), source
			switch form {
			case "absolute input":
				dataset = source
			case "normalized dots":
				dataset = "shared files" + string(filepath.Separator) + ".." + string(filepath.Separator) +
					filepath.Join("shared files", "shared391.yml")
			case "Windows case":
				destination = strings.ToUpper(source)
			case "hard link", "symlink":
				destination = filepath.Join(dir, "output.yml")
				var err error
				if form == "hard link" {
					err = os.Link(source, destination)
				} else {
					err = os.Symlink("shared391.yml", destination)
				}
				if err != nil {
					t.Skipf("owned temporary %s is unavailable: %v", form, err)
				}
			}
			before := initFileSnapshot(t, h.dir)
			root := NewRootCommand()
			root.SetContext(t.Context())
			root.SilenceErrors, root.SilenceUsage = true, true
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&bytes.Buffer{})
			root.SetArgs([]string{"init", "--name", "same-file", "--conversation-mode", "static",
				"--dataset", dataset, "--path", destination, "--judge-model", "judge", "--no-prompt", "-o", "json"})
			err := root.Execute()
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "same-file authoring must fail with a structured conflict: %v", err)
			assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
			assert.Contains(t, local.Suggestion, "--path")
			var doc jsonError
			require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
			assert.Contains(t, doc.Error.Message, "same file")
			assert.Equal(t, before, initFileSnapshot(t, h.dir), "preserve synthetic CRLF bytes and every owned path")
			assert.Zero(t, h.project.wiringAttempts())
			assert.Empty(t, h.usage.reported())
		})
	}
}

func TestInitRechecksDatasetDestinationAfterConfirmation(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	prompts := &conversationPromptServer{}
	h := newInitHarness(t, nil, prompts)
	require.NoError(t, os.WriteFile(h.seedRows, []byte(sameFileConversationRows), 0o600))
	destination := filepath.Join(h.dir, "quality", "custom.yml")
	unlock, err := project.LockEvalConfig(t.Context(), destination)
	require.NoError(t, err)
	unlock()
	if err := os.Link(h.seedRows, destination); err != nil {
		t.Skipf("owned temporary hard links are unavailable: %v", err)
	}
	require.NoError(t, os.Remove(destination))
	afterSwap := make(chan map[string]string, 1)
	prompts.onConfirm = func() error {
		if err := os.Link(h.seedRows, destination); err != nil {
			return err
		}
		afterSwap <- initFileSnapshot(t, h.dir)
		return nil
	}
	_, err = executeConversationInit(t, "--name", "late-alias", "--conversation-mode", "static",
		"--dataset", h.seedRows, "--path", destination, "--judge-model", "judge")
	require.ErrorContains(t, err, "same file")
	assert.Equal(t, <-afterSwap, initFileSnapshot(t, h.dir), "only the concurrent alias creation may survive")
	assert.Zero(t, h.project.wiringAttempts())
}

func TestInitDistinctDatasetDestinationPreservesLazyReferences(t *testing.T) {
	h := newInitHarness(t, nil)
	require.NoError(t, os.WriteFile(h.seedRows, []byte(sameFileConversationRows), 0o600))
	destination := filepath.Join(h.dir, "quality files", "custom.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(destination), 0o700))
	original := "# keep authored metadata\nx-owner: team\ndatasets:\n" +
		"  - name: unrelated\n    $ref: ./not-present.yml\n"
	require.NoError(t, os.WriteFile(destination, []byte(original), 0o600))
	_, err := executeConversationInit(t, "--name", "distinct-files", "--conversation-mode", "static",
		"--dataset", h.seedRows, "--path", destination, "--judge-model", "judge", "--no-prompt", "-o", "json")
	require.NoError(t, err)
	source, err := os.ReadFile(h.seedRows)
	require.NoError(t, err)
	assert.Equal(t, sameFileConversationRows, string(source))
	body, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Contains(t, string(body), original)
	assert.Contains(t, string(body), "name: distinct-files")
	assert.Equal(t, 1, h.project.wiringAttempts())
}
