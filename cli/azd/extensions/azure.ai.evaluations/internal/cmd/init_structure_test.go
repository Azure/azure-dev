// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitRefusesSymlinkConfigBeforeWriting(t *testing.T) {
	h := newInitHarness(t, nil)
	require.NoError(t, os.WriteFile(h.seedRows, []byte("{\"messages\":[]}\n"), 0o600))
	dir := filepath.Join(h.dir, project.DefaultEvalDir)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	target := filepath.Join(h.dir, "shared.yml")
	original := "# shared config\nevals: []\n"
	require.NoError(t, os.WriteFile(target, []byte(original), 0o600))
	configPath := filepath.Join(dir, project.EvalConfigBase)
	linkTarget := filepath.Join("..", "shared.yml")
	if err := os.Symlink(linkTarget, configPath); err != nil {
		t.Skipf("creating test symlinks is unavailable: %v", err)
	}
	before := initFileSnapshot(t, h.dir)
	text, err := executeConversationInit(t, "--path", configPath, "--name", "new-eval",
		"--conversation-mode", "static", "--dataset", h.seedRows, "--judge-model", "judge",
		"--no-prompt", "-o", "json")
	require.ErrorContains(t, err, "symbolic link")
	assert.Empty(t, text)
	assert.Zero(t, h.project.wiringAttempts())
	assert.Empty(t, h.usage.reported())
	assert.Equal(t, before, initFileSnapshot(t, h.dir))
	got, err := os.Readlink(configPath)
	require.NoError(t, err)
	assert.Equal(t, linkTarget, got)
	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, original, string(body))

	_, err = executeConversationInit(t, "--path", target, "--name", "new-eval",
		"--conversation-mode", "static", "--dataset", h.seedRows, "--judge-model", "judge",
		"--no-prompt", "-o", "json")
	require.NoError(t, err, "selecting the target directly must allow the otherwise valid init")
	assert.Equal(t, 1, h.project.wiringAttempts())
	got, err = os.Readlink(configPath)
	require.NoError(t, err)
	assert.Equal(t, linkTarget, got)
	cfg, err := project.ReadAuthoredConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"new-eval"}, cfg.Names(project.SectionEvals))
}

func TestInitRefusesSelectedDirectorySymlinkBeforeWriting(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, selection := range []string{"plain", "trailing separator", "dot suffix", "normalized dots"} {
			for _, format := range []string{"default", "json"} {
				t.Run(fmt.Sprintf("existing=%t/%s/%s", existing, selection, format), func(t *testing.T) {
					h := newInitHarness(t, nil)
					require.NoError(t, os.WriteFile(h.seedRows, []byte("{\"messages\":[]}\n"), 0o600))
					target := t.TempDir()
					configPath := filepath.Join(target, project.EvalConfigBase)
					if existing {
						require.NoError(t, os.WriteFile(configPath, []byte("# shared config\nevals: []\n"), 0o600))
					}
					beforeProject := initFileSnapshot(t, h.dir)
					beforeTarget := initFileSnapshot(t, target)
					link := filepath.Join(h.dir, "selected directory")
					if err := os.Symlink(target, link); err != nil {
						t.Skipf("creating test symlinks is unavailable: %v", err)
					}
					location := link
					separator := string(filepath.Separator)
					switch selection {
					case "trailing separator":
						location += separator
					case "dot suffix":
						location += separator + "."
					case "normalized dots":
						location = h.dir + separator + "." + separator + filepath.Base(link)
					}
					text, err := executeConversationInit(t, "--path", location, "--name", "new-eval",
						"--conversation-mode", "static", "--dataset", h.seedRows, "--judge-model", "judge",
						"--no-prompt", "--output", format)
					assert.ErrorContains(t, err, "symbolic link")
					assert.Empty(t, text)
					assert.Zero(t, h.project.wiringAttempts())
					assert.Empty(t, h.usage.reported())
					assert.Equal(t, beforeTarget, initFileSnapshot(t, target))
					got, err := os.Readlink(link)
					require.NoError(t, err)
					assert.Equal(t, target, got)
					require.NoError(t, os.Remove(link))
					assert.Equal(t, beforeProject, initFileSnapshot(t, h.dir))

					_, err = executeConversationInit(t, "--path", target, "--name", "new-eval",
						"--conversation-mode", "static", "--dataset", h.seedRows, "--judge-model", "judge",
						"--no-prompt", "--output", format)
					require.NoError(t, err, "selecting the real directory directly must still allow init")
					assert.Equal(t, 1, h.project.wiringAttempts())
					cfg, err := project.ReadAuthoredConfig(configPath)
					require.NoError(t, err)
					assert.Equal(t, []string{"new-eval"}, cfg.Names(project.SectionEvals))
				})
			}
		}
	}
}

func TestInitRefusesAmbiguousAuthoredDocumentsBeforeWriting(t *testing.T) {
	for _, shape := range []string{
		"single document", "second document", "explicit end then second", "duplicate datasets", "merged catalogs",
	} {
		t.Run(shape, func(t *testing.T) {
			h := newInitHarness(t, nil)
			if shape == "single document" {
				require.NoError(t, os.WriteFile(h.seedRows, []byte("{\"messages\":[]}\n"), 0o600))
			}
			dir := filepath.Join(h.dir, project.DefaultEvalDir)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			configPath := filepath.Join(dir, project.EvalConfigBase)
			first := fmt.Sprintf("# preserve authored content\ndatasets:\n  - name: golden\n    file: %q\n"+
				"evals:\n  - name: existing\n    dataset: golden\n", filepath.ToSlash(h.seedRows))
			second := "datasets:\n  - name: other-owned\n    file: ./other.jsonl\n" +
				"evals:\n  - name: other-eval\n    dataset: other-owned\n"
			body := first
			switch shape {
			case "second document":
				body += "---\n" + second
			case "explicit end then second":
				body += "...\n---\n" + second
			case "duplicate datasets":
				body += "datasets:\n  - name: other-owned\n    file: ./other.jsonl\n"
			case "merged catalogs":
				body = fmt.Sprintf("x-defaults: &defaults\n  datasets:\n    - name: golden\n      file: %q\n"+
					"  evals:\n    - name: other-eval\n      dataset: golden\n<<: *defaults\n",
					filepath.ToSlash(h.seedRows))
			}
			require.NoError(t, os.WriteFile(configPath, []byte(body), 0o600))
			before := initFileSnapshot(t, h.dir)
			text, err := executeConversationInit(t, "--path", configPath, "--name", "new-eval",
				"--conversation-mode", "static", "--dataset", "golden", "--judge-model", "judge",
				"--no-prompt", "-o", "json")
			if shape == "single document" {
				require.NoError(t, err)
				authored, err := project.ReadAuthoredConfig(configPath)
				require.NoError(t, err)
				assert.Equal(t, []string{"existing", "new-eval"}, authored.Names(project.SectionEvals))
				return
			}
			if err == nil && shape != "duplicate datasets" {
				after, readErr := os.ReadFile(configPath)
				require.NoError(t, readErr)
				assert.Contains(t, string(after), "other-eval", "the second document's owned eval must not be discarded")
			}
			require.Error(t, err, "unsupported or ambiguous documents must not be silently rewritten")
			assert.Empty(t, text)
			assert.Zero(t, h.project.wiringAttempts())
			assert.Empty(t, h.usage.reported())
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
		})
	}
}
