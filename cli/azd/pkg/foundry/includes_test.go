// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

)

// writeFile writes content to dir/name (creating parent directories) and returns nothing; it
// is used to build $ref fixture trees under a temp project root.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// parseYAML decodes a YAML document into a map, matching how the Foundry config reaches the
// extension (already-parsed data).
func parseYAML(t *testing.T, content string) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(content), &out))
	return out
}

// requireFileRefError asserts err is an invalid_file_ref validation error whose message
// contains substr.
func requireFileRefError(t *testing.T, err error, substr string) {
	t.Helper()
	require.Error(t, err)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, CodeInvalidFileRef, localErr.Code)
	assert.Contains(t, localErr.Message, substr)
}

func TestResolveFileRefs_NoRefsIsUnchanged(t *testing.T) {
	root := t.TempDir()
	cfg := parseYAML(t, `
endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
agents:
  - name: a
    kind: hosted
    project: src/a
  - name: b
    kind: prompt
    instructions: prompts/b.md
`)
	want := parseYAML(t, `
endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
agents:
  - name: a
    kind: hosted
    project: src/a
  - name: b
    kind: prompt
    instructions: prompts/b.md
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)
	// Inline path values authored directly in azure.yaml are left exactly as written.
	assert.Equal(t, want, got)
}

func TestResolveFileRefs_NilConfig(t *testing.T) {
	got, err := ResolveFileRefs(nil, t.TempDir())
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestResolveFileRefs_RelativeIncludeNoSiblings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/support-agent.yaml", `
name: support-agent
kind: hosted
project: ../src/support-agent
docker:
  path: Dockerfile
`)
	cfg := parseYAML(t, `
agents:
  - $ref: ./agents/support-agent.yaml
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	// project rebased from the agents/ directory to the project root; docker.path is not a
	// path-bearing key and is left untouched.
	want := parseYAML(t, `
agents:
  - name: support-agent
    kind: hosted
    project: src/support-agent
    docker:
      path: Dockerfile
`)
	assert.Equal(t, want, got)
}

func TestResolveFileRefs_RefWithSurroundingWhitespaceResolves(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/a.yaml", `
name: a
kind: hosted
project: ../src/a
`)
	cfg := parseYAML(t, `
agents:
  - $ref: "  ./agents/a.yaml  "
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	// Surrounding whitespace in the $ref value is trimmed before the path is resolved.
	want := parseYAML(t, `
agents:
  - name: a
    kind: hosted
    project: src/a
`)
	assert.Equal(t, want, got)
}

func TestResolveFileRefs_SiblingOverlayOverrides(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/base.yaml", `
name: base
kind: hosted
project: ../src/base
protocols:
  - protocol: responses
    version: "1.0.0"
env:
  A: "1"
  B: "2"
`)
	cfg := parseYAML(t, `
agents:
  - $ref: ./agents/base.yaml
    name: override
    env:
      C: "3"
    protocols:
      - protocol: a2a
        version: "2.0.0"
    extra: true
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	// Scalar (name) replaced, map (env) and array (protocols) replaced wholesale (shallow),
	// new key (extra) added, and the un-overridden project is still rebased.
	want := parseYAML(t, `
agents:
  - name: override
    kind: hosted
    project: src/base
    protocols:
      - protocol: a2a
        version: "2.0.0"
    env:
      C: "3"
    extra: true
`)
	assert.Equal(t, want, got)
}

func TestResolveFileRefs_NestedIncludeRebasesPerFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/a.yaml", `
name: a
kind: hosted
project: ../src/a
skill:
  $ref: ../skills/s.yaml
`)
	writeFile(t, root, "skills/s.yaml", `
name: s
instructions: ../prompts/s.md
`)
	cfg := parseYAML(t, `
agents:
  - $ref: ./agents/a.yaml
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	// a.yaml's project rebases from agents/; the nested skill from skills/ rebases its
	// instructions path relative to skills/, both anchored back to the project root.
	want := parseYAML(t, `
agents:
  - name: a
    kind: hosted
    project: src/a
    skill:
      name: s
      instructions: prompts/s.md
`)
	assert.Equal(t, want, got)
}

func TestResolveFileRefs_InstructionsInlineVsPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "skills/path.yaml", `
name: path-skill
instructions: ../prompts/code-review.md
`)
	writeFile(t, root, "skills/inline.yaml", `
name: inline-skill
instructions: |
  Review the code for style issues.
  Keep it terse.
`)
	cfg := parseYAML(t, `
skills:
  - $ref: ./skills/path.yaml
  - $ref: ./skills/inline.yaml
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	// A single-line .md value is treated as a path and rebased; multi-line inline prose is
	// left untouched.
	want := parseYAML(t, `
skills:
  - name: path-skill
    instructions: prompts/code-review.md
  - name: inline-skill
    instructions: |
      Review the code for style issues.
      Keep it terse.
`)
	assert.Equal(t, want, got)
}

func TestResolveFileRefs_AbsolutePathInclude(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/abs.yaml", `
name: abs
kind: prompt
instructions: hi there
`)
	absRef := filepath.ToSlash(filepath.Join(root, "agents", "abs.yaml"))
	cfg := map[string]any{
		"agents": []any{
			map[string]any{refKey: absRef},
		},
	}

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	want := parseYAML(t, `
agents:
  - name: abs
    kind: prompt
    instructions: hi there
`)
	assert.Equal(t, want, got)
}

func TestResolveFileRefs_JSONInclude(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/j.json", `{"name":"j","kind":"prompt","instructions":"hello"}`)
	cfg := parseYAML(t, `
agents:
  - $ref: ./agents/j.json
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	want := parseYAML(t, `
agents:
  - name: j
    kind: prompt
    instructions: hello
`)
	assert.Equal(t, want, got)
}

func TestResolveFileRefs_MissingFile(t *testing.T) {
	root := t.TempDir()
	cfg := parseYAML(t, `
agents:
  - $ref: ./agents/does-not-exist.yaml
`)

	_, err := ResolveFileRefs(cfg, root)
	requireFileRefError(t, err, "cannot read")
	assert.Contains(t, err.Error(), "does-not-exist.yaml")
}

func TestResolveFileRefs_MalformedFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/bad.yaml", "name: support\n  bad: : indentation: [")
	cfg := parseYAML(t, `
agents:
  - $ref: ./agents/bad.yaml
`)

	_, err := ResolveFileRefs(cfg, root)
	requireFileRefError(t, err, "not a valid YAML or JSON object")
}

func TestResolveFileRefs_TopLevelSequenceFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/list.yaml", "- one\n- two\n")
	cfg := parseYAML(t, `
agents:
  - $ref: ./agents/list.yaml
`)

	_, err := ResolveFileRefs(cfg, root)
	// A top-level sequence cannot decode into a mapping.
	requireFileRefError(t, err, "list.yaml")
}

func TestResolveFileRefs_CycleDetected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.yaml", `
name: a
next:
  $ref: ./b.yaml
`)
	writeFile(t, root, "b.yaml", `
name: b
next:
  $ref: ./a.yaml
`)
	cfg := parseYAML(t, `
agents:
  - $ref: ./a.yaml
`)

	_, err := ResolveFileRefs(cfg, root)
	requireFileRefError(t, err, "cyclic")
}

func TestResolveFileRefs_URLRejected(t *testing.T) {
	root := t.TempDir()
	cfg := parseYAML(t, `
agents:
  - $ref: https://example.com/agents/a.yaml
`)

	_, err := ResolveFileRefs(cfg, root)
	requireFileRefError(t, err, "remote includes are not supported")
}

func TestResolveFileRefs_NonStringRef(t *testing.T) {
	root := t.TempDir()
	cfg := parseYAML(t, `
agents:
  - $ref: 123
`)

	_, err := ResolveFileRefs(cfg, root)
	requireFileRefError(t, err, "must be a string")
}

func TestResolveFileRefs_EmptyRef(t *testing.T) {
	root := t.TempDir()
	cfg := parseYAML(t, `
agents:
  - $ref: ""
`)

	_, err := ResolveFileRefs(cfg, root)
	requireFileRefError(t, err, "must not be empty")
}

// In the separate-services shape an agent's body lives in its own file and the inline map is
// itself the $ref directive (the host and service key are stripped by core before the entry
// reaches the extension), so the whole entry resolves from the file.
func TestResolveFileRefs_ServiceEntryTopLevelRef(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/research-agent.yaml", `
kind: hosted
project: ../src/research-agent
docker:
  path: Dockerfile
`)
	cfg := parseYAML(t, `
$ref: ./agents/research-agent.yaml
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	// project rebased from the agents/ directory to the project root; docker.path is not a
	// path-bearing key and is left untouched.
	want := parseYAML(t, `
kind: hosted
project: src/research-agent
docker:
  path: Dockerfile
`)
	assert.Equal(t, want, got)
}

// A service-entry-level $ref may carry sibling overrides authored inline in azure.yaml, which
// overlay the loaded file shallowly. Inline values are left exactly as written; only the file's
// own paths rebase.
func TestResolveFileRefs_ServiceEntryTopLevelRefWithOverlay(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/research-agent.yaml", `
kind: hosted
project: ../src/research-agent
env:
  A: "1"
`)
	cfg := parseYAML(t, `
$ref: ./agents/research-agent.yaml
kind: prompt
instructions: Do the thing inline.
env:
  B: "2"
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	// kind scalar overridden, env map replaced wholesale (shallow), inline instructions prose
	// kept as-is (not a .md/.txt path, so not rebased), and the file's project still rebases.
	want := parseYAML(t, `
kind: prompt
project: src/research-agent
instructions: Do the thing inline.
env:
  B: "2"
`)
	assert.Equal(t, want, got)
}

// Deployments stay an array on the project service, so a deployment $ref sits at the array-item
// level. Each item resolves independently; inline items pass through unchanged.
func TestResolveFileRefs_ProjectDeploymentsArrayItemRef(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "deployments/gpt-4o.yaml", `
name: gpt-4o
model:
  name: gpt-4o
  format: OpenAI
  version: "2024-08-06"
sku:
  name: GlobalStandard
  capacity: 10
`)
	cfg := parseYAML(t, `
endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
deployments:
  - $ref: ./deployments/gpt-4o.yaml
  - name: text-embedding-3-large
    model:
      name: text-embedding-3-large
      format: OpenAI
      version: "1"
    sku:
      name: Standard
      capacity: 50
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	want := parseYAML(t, `
endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
deployments:
  - name: gpt-4o
    model:
      name: gpt-4o
      format: OpenAI
      version: "2024-08-06"
    sku:
      name: GlobalStandard
      capacity: 10
  - name: text-embedding-3-large
    model:
      name: text-embedding-3-large
      format: OpenAI
      version: "1"
    sku:
      name: Standard
      capacity: 50
`)
	assert.Equal(t, want, got)
}

// A deployment array-item $ref may carry sibling overrides, which overlay the loaded file
// shallowly: scalars replace, and a sibling map replaces the loaded map wholesale.
func TestResolveFileRefs_DeploymentRefOverlay(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "deployments/base.yaml", `
name: base-name
model:
  name: gpt-4o
  format: OpenAI
  version: "2024-08-06"
sku:
  name: Standard
  capacity: 10
`)
	cfg := parseYAML(t, `
deployments:
  - $ref: ./deployments/base.yaml
    name: overridden
    sku:
      name: GlobalStandard
      capacity: 100
`)

	got, err := ResolveFileRefs(cfg, root)
	require.NoError(t, err)

	// name scalar overridden, sku map replaced wholesale, model left untouched.
	want := parseYAML(t, `
deployments:
  - name: overridden
    model:
      name: gpt-4o
      format: OpenAI
      version: "2024-08-06"
    sku:
      name: GlobalStandard
      capacity: 100
`)
	assert.Equal(t, want, got)
}
