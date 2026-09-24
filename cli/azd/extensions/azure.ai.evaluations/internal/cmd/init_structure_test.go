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

func TestInitRefusesAmbiguousAuthoredDocumentsBeforeWriting(t *testing.T) {
	for _, shape := range []string{
		"single document", "second document", "explicit end then second", "duplicate datasets", "merged catalogs",
	} {
		t.Run(shape, func(t *testing.T) {
			h := newInitHarness(t, nil)
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
