// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The README's configuration example has to be one the CLI can load.
//
// It documented `options.eval_model`, which is not a key this decoder has ever
// had: following the README produced `unknown key "options"`. Nothing caught it
// because the example was prose, so correcting the keys alone would only have
// reset the clock on the same drift.
func TestTheREADMEExampleLoads(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)

	body := evalConfigExample(t, string(readme))

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	require.NoError(t, root.WriteFile(EvalConfigBase, []byte(body), 0o600))

	_, err = LoadEvalConfig(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err, "the README example has to survive the decoder it documents")
}

// evalConfigExample returns the fenced yaml block the README labels as the eval
// configuration, identified by the file name comment on its first line.
func evalConfigExample(t *testing.T, readme string) string {
	t.Helper()

	const marker = "```yaml\n# evals/" + EvalConfigBase + "\n"

	readme = strings.ReplaceAll(readme, "\r\n", "\n")

	start := strings.Index(readme, marker)
	require.NotEqual(t, -1, start,
		"the README no longer opens the example with `# evals/%s`; retarget this test "+
			"rather than deleting it", EvalConfigBase)

	rest := readme[start+len(marker):]
	end := strings.Index(rest, "```")
	require.NotEqual(t, -1, end, "the example's fence is unterminated")

	return rest[:end]
}
