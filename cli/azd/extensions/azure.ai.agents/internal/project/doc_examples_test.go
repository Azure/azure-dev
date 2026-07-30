// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

// This file validates the `azure.yaml` examples embedded in this extension's
// markdown docs. Doc snippets used to drift from the code with nothing in CI to
// catch it: the README's migration example once omitted the required agent
// `name`, so copying it failed with "name cannot be empty". See issue #9330.
//
// Two checks run against every fenced YAML block that declares an
// `azure.ai.agent` service:
//
//  1. Resolver — the snippet must survive [AgentDefinitionFromService], the same
//     entry point azd uses at deploy time.
//  2. Vocabulary — every property must be one azd actually understands. azd
//     ignores unrecognized service properties at runtime (deliberately, for
//     forward compatibility), which is how a doc can advertise a setting that
//     silently does nothing. Our own docs should not rely on that leniency.

// docExamplePartialMarker opts a snippet out of the "must fully resolve" check.
//
// Not every example is meant to be complete. The `azure.ai.agent` entries in
// docs/private-networking.md intentionally omit `kind` because the snippet is
// about the project's network block; azd falls back to the on-disk agent.yaml.
// Place this marker on its own line immediately before the opening fence.
const docExamplePartialMarker = "<!-- azd:doc-example partial -->"

// coreServiceKeys are the `services.<name>` properties that azd core parses into
// typed fields on its own ServiceConfig. Everything else in a service block is
// captured by core's `yaml:",inline"` AdditionalProperties map and handed to the
// extension. Mirrors ServiceConfig in cli/azd/pkg/project/service_config.go.
var coreServiceKeys = []string{
	"apiVersion", "condition", "config", "dist", "docker", "env", "hooks", "host",
	"image", "infra", "k8s", "language", "module", "project", "remoteBuild",
	"resourceGroup", "resourceName", "uses",
}

// docExample is a single fenced YAML block extracted from a markdown file.
type docExample struct {
	file    string
	line    int
	content string
	partial bool
}

// String identifies the snippet in test output as file:line, so a failure points
// straight at the fence that needs fixing.
func (e docExample) String() string {
	return fmt.Sprintf("%s:%d", e.file, e.line)
}

// extensionRoot returns the extension's root directory (the parent of internal/).
func extensionRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return root
}

// extractYAMLExamples returns every fenced YAML block in the markdown source.
// Fences indented inside list items are dedented by the fence's own indent so
// the captured content parses as standalone YAML.
func extractYAMLExamples(file, source string) []docExample {
	var examples []docExample

	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	partial := false

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		if trimmed == docExamplePartialMarker {
			partial = true
			continue
		}

		open := countLeadingBackticks(trimmed)
		if open < 3 {
			// Only a blank line may sit between the marker and its fence.
			if trimmed != "" {
				partial = false
			}
			continue
		}

		lang := strings.TrimSpace(trimmed[open:])
		indent := strings.Index(lines[i], "`")
		start := i + 1

		// Per CommonMark, a fence closes only on a run of at least as many
		// backticks as opened it, so a longer fence can wrap shorter ones.
		var body []string
		for i++; i < len(lines) && !closesFence(lines[i], open); i++ {
			body = append(body, dedent(lines[i], indent))
		}

		if lang == "yaml" || lang == "yml" {
			examples = append(examples, docExample{
				file:    file,
				line:    start,
				content: strings.Join(body, "\n"),
				partial: partial,
			})
		}
		partial = false
	}

	return examples
}

// countLeadingBackticks returns the length of the backtick run at the start of line.
func countLeadingBackticks(line string) int {
	n := 0
	for n < len(line) && line[n] == '`' {
		n++
	}
	return n
}

// closesFence reports whether line is a bare run of at least open backticks.
func closesFence(line string, open int) bool {
	trimmed := strings.TrimSpace(line)
	n := countLeadingBackticks(trimmed)
	return n >= open && n == len(trimmed)
}

// dedent removes up to n leading spaces from line.
func dedent(line string, n int) string {
	for range n {
		if !strings.HasPrefix(line, " ") {
			break
		}
		line = line[1:]
	}
	return line
}

// agentServiceKeys returns the property names documented for the
// `azure.ai.agent` service block, read from the extension's published schema.
func agentServiceKeys(t *testing.T, root string) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(root, "schemas", "azure.ai.agent.json"))
	require.NoError(t, err)

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	require.NotEmpty(t, schema.Properties, "azure.ai.agent.json declares no properties")

	keys := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		keys = append(keys, k)
	}
	return keys
}

// serviceConfigFromDoc builds the ServiceConfig azd core would hand this
// extension for the given service block, applying core's split: typed fields for
// known keys, AdditionalProperties for everything else.
func serviceConfigFromDoc(t *testing.T, name string, svc map[string]any) *azdext.ServiceConfig {
	t.Helper()

	str := func(key string) string {
		s, _ := svc[key].(string)
		return s
	}

	extra := map[string]any{}
	for k, v := range svc {
		if !slices.Contains(coreServiceKeys, k) {
			extra[k] = v
		}
	}

	props, err := structpb.NewStruct(extra)
	require.NoError(t, err)

	out := &azdext.ServiceConfig{
		Name:                 name,
		Host:                 str("host"),
		Language:             str("language"),
		RelativePath:         str("project"),
		Image:                str("image"),
		AdditionalProperties: props,
	}

	// The deprecated shape nests the agent definition under `config`.
	if cfg, ok := svc["config"].(map[string]any); ok {
		legacy, err := structpb.NewStruct(cfg)
		require.NoError(t, err)
		out.Config = legacy
	}

	return out
}

// agentServicesInExample returns the `azure.ai.agent` service blocks declared by
// the snippet, keyed by service name.
func agentServicesInExample(t *testing.T, e docExample) map[string]map[string]any {
	t.Helper()

	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(e.content), &doc), "%s: snippet is not valid YAML", e)

	found := map[string]map[string]any{}
	for name, svc := range doc.Services {
		if host, _ := svc["host"].(string); host == "azure.ai.agent" {
			found[name] = svc
		}
	}
	return found
}

// TestDocExamplesAreValid runs every `azure.ai.agent` snippet in this
// extension's docs through the resolver azd uses at deploy time, so a doc a user
// copies into azure.yaml cannot drift into a broken state unnoticed.
func TestDocExamplesAreValid(t *testing.T) {
	t.Parallel()

	root := extensionRoot(t)
	knownKeys := append(agentServiceKeys(t, root), coreServiceKeys...)

	var files []string
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, path)
		}
		return nil
	}))

	var examples []docExample
	for _, path := range files {
		source, err := os.ReadFile(path)
		require.NoError(t, err)

		rel, err := filepath.Rel(root, path)
		require.NoError(t, err)

		examples = append(examples, extractYAMLExamples(filepath.ToSlash(rel), string(source))...)
	}

	checked := 0
	for _, e := range examples {
		if !strings.Contains(e.content, "azure.ai.agent") {
			continue
		}

		for name, svc := range agentServicesInExample(t, e) {
			checked++

			t.Run(fmt.Sprintf("%s/%s", e, name), func(t *testing.T) {
				t.Parallel()

				cfg := serviceConfigFromDoc(t, name, svc)
				_, _, found, _, err := AgentDefinitionFromService(cfg)

				require.NoError(t, err,
					"%s: service %q is not a valid agent definition. "+
						"Fix the example so it can be copied into azure.yaml as-is.", e, name)

				if !e.partial {
					require.True(t, found,
						"%s: service %q did not resolve to an agent definition (is `kind` missing?). "+
							"If the snippet is deliberately incomplete, put %s on the line before the fence.",
						e, name, docExamplePartialMarker)
				}

				// Guard the silent-no-op class of defect: azd ignores properties
				// it does not recognize, so an undocumented key deploys cleanly
				// while doing nothing at all.
				for key := range definitionProps(svc) {
					require.Contains(t, knownKeys, key,
						"%s: service %q documents property %q, which azd does not support. "+
							"azd ignores unknown properties, so users copying this get no error and no effect.",
						e, name, key)
				}
			})
		}
	}

	require.NotZero(t, checked, "no azure.ai.agent doc examples were found — is the extractor still working?")
}

// definitionProps returns the properties carrying the agent definition: the
// service block itself, or its `config` child for the deprecated nested shape.
func definitionProps(svc map[string]any) map[string]any {
	if cfg, ok := svc["config"].(map[string]any); ok {
		if _, nested := cfg["kind"]; nested {
			return cfg
		}
	}
	return svc
}

// TestExtractYAMLExamples covers the extractor itself, since every other
// assertion in this file depends on it finding the right blocks.
func TestExtractYAMLExamples(t *testing.T) {
	t.Parallel()

	source := strings.Join([]string{
		"# Doc",
		"",
		"```yaml",
		"services:",
		"  a:",
		"    host: azure.ai.agent",
		"```",
		"",
		"```bash",
		"not: yaml",
		"```",
		"",
		docExamplePartialMarker,
		"```yaml",
		"services:",
		"  b:",
		"    host: azure.ai.agent",
		"```",
		"",
		"- list item:",
		"",
		"  ```yaml",
		"  services:",
		"    c:",
		"      host: azure.ai.agent",
		"  ```",
		"",
		"````markdown",
		docExamplePartialMarker,
		"```yaml",
		"services:",
		"  d:",
		"    host: azure.ai.agent",
		"```",
		"````",
	}, "\n")

	got := extractYAMLExamples("doc.md", source)
	require.Len(t, got, 3, "bash block must be skipped; fence inside a longer fence must not be extracted")

	require.False(t, got[0].partial)
	require.Equal(t, 3, got[0].line)

	require.True(t, got[1].partial, "marker must apply to the fence that follows it")

	require.False(t, got[2].partial, "marker must not leak to a later fence")
	require.Equal(t, "services:\n  c:\n    host: azure.ai.agent", got[2].content,
		"indented fences must be dedented")
}
