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

// docSchema is the extension's published JSON Schema for the `azure.ai.agent`
// service block, used to check that every property a doc advertises is one the
// extension actually declares — including properties nested inside documented
// objects, which the non-strict unmarshalling this test guards against would
// otherwise drop silently.
type docSchema struct {
	root map[string]any
}

// loadDocSchema reads schemas/azure.ai.agent.json from the extension root.
func loadDocSchema(t *testing.T, root string) *docSchema {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(root, "schemas", "azure.ai.agent.json"))
	require.NoError(t, err)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))

	s := &docSchema{root: schema}
	require.NotEmpty(t, s.properties(schema), "azure.ai.agent.json declares no properties")
	return s
}

// property returns the schema node declared for a top-level service property.
func (s *docSchema) property(key string) (map[string]any, bool) {
	node, ok := s.properties(s.root)[key].(map[string]any)
	return node, ok
}

// properties returns the `properties` map of a schema node, resolving nothing.
func (s *docSchema) properties(node map[string]any) map[string]any {
	props, _ := node["properties"].(map[string]any)
	return props
}

// resolve follows a local `$ref` (`#/definitions/<name>`) to the node it names.
func (s *docSchema) resolve(node map[string]any) map[string]any {
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}

	name := strings.TrimPrefix(ref, "#/definitions/")
	defs, _ := s.root["definitions"].(map[string]any)
	if target, ok := defs[name].(map[string]any); ok {
		return s.resolve(target)
	}
	return node
}

// checkValue asserts that value contains no property the schema node does not
// declare, recursing through objects and array items. A node that permits
// additional properties (the JSON Schema default) is not narrowed further.
func (s *docSchema) checkValue(t *testing.T, e docExample, name, path string, node map[string]any, value any) {
	t.Helper()

	node = s.resolve(node)

	switch v := value.(type) {
	case map[string]any:
		props := s.properties(node)
		additional, declared := node["additionalProperties"]

		for key, child := range v {
			childPath := path + "." + key

			if sub, ok := props[key].(map[string]any); ok {
				s.checkValue(t, e, name, childPath, sub, child)
				continue
			}
			if sub, ok := additional.(map[string]any); ok {
				s.checkValue(t, e, name, childPath, sub, child)
				continue
			}
			if allowed, ok := additional.(bool); !declared || (ok && allowed) {
				continue
			}
			require.Fail(t, "undeclared property", undeclaredPropertyMessage(e, name, childPath))
		}
	case []any:
		items, ok := node["items"].(map[string]any)
		if !ok {
			return
		}
		for i, item := range v {
			s.checkValue(t, e, name, fmt.Sprintf("%s[%d]", path, i), items, item)
		}
	}
}

// undeclaredPropertyMessage explains why an unsupported property is a defect
// rather than a harmless extra, since azd itself reports nothing.
func undeclaredPropertyMessage(e docExample, name, path string) string {
	return fmt.Sprintf(
		"%s: service %q documents property %q, which azd does not support. "+
			"azd ignores unknown properties, so users copying this get no error and no effect.",
		e, name, path)
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

// coreServiceFields mirrors the typed fields azd core parses out of a
// `services.<name>` block, following ServiceConfig in
// cli/azd/pkg/project/service_config.go. Decoding a snippet through it applies
// core's own shape rules — `project: [src]` is a type error for core and must be
// one here too — instead of silently coercing a malformed value to its zero
// value. Everything core does not name lands in AdditionalProperties, exactly as
// core's `yaml:",inline"` field does before the block reaches this extension.
//
// Core's package is mirrored rather than imported: it is not part of this
// module's dependency surface, and pulling it in for a docs test would drag
// azd's provisioning tree behind it.
type coreServiceFields struct {
	ResourceGroupName    string            `yaml:"resourceGroup"`
	ResourceName         string            `yaml:"resourceName"`
	ApiVersion           string            `yaml:"apiVersion"`
	RelativePath         string            `yaml:"project"`
	Host                 string            `yaml:"host"`
	Language             string            `yaml:"language"`
	OutputPath           string            `yaml:"dist"`
	Image                string            `yaml:"image"`
	Docker               map[string]any    `yaml:"docker"`
	K8s                  map[string]any    `yaml:"k8s"`
	Module               string            `yaml:"module"`
	Infra                map[string]any    `yaml:"infra"`
	Hooks                map[string]any    `yaml:"hooks"`
	Uses                 []string          `yaml:"uses"`
	Config               map[string]any    `yaml:"config"`
	Environment          map[string]string `yaml:"env"`
	Condition            string            `yaml:"condition"`
	RemoteBuild          *bool             `yaml:"remoteBuild"`
	AdditionalProperties map[string]any    `yaml:",inline"`
}

// serviceConfigFromDoc builds the ServiceConfig azd core would hand this
// extension for the given service block, failing the test if any field core
// itself parses has a shape core would reject.
func serviceConfigFromDoc(t *testing.T, e docExample, name string, svc map[string]any) *azdext.ServiceConfig {
	t.Helper()

	raw, err := yaml.Marshal(svc)
	require.NoError(t, err)

	var core coreServiceFields
	require.NoError(t, yaml.Unmarshal(raw, &core),
		"%s: service %q cannot be parsed by azd core. "+
			"Fix the example so it can be copied into azure.yaml as-is.", e, name)

	props, err := structpb.NewStruct(core.AdditionalProperties)
	require.NoError(t, err,
		"%s: service %q has a value that cannot be represented in azure.yaml "+
			"(quote ambiguous scalars such as dates).", e, name)

	out := &azdext.ServiceConfig{
		Name:                 name,
		Host:                 core.Host,
		Language:             core.Language,
		RelativePath:         core.RelativePath,
		Image:                core.Image,
		AdditionalProperties: props,
	}

	// The deprecated shape nests the agent definition under `config`.
	if core.Config != nil {
		legacy, err := structpb.NewStruct(core.Config)
		require.NoError(t, err,
			"%s: service %q has a value under `config` that cannot be represented in "+
				"azure.yaml (quote ambiguous scalars such as dates).", e, name)
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
	schema := loadDocSchema(t, root)

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

				cfg := serviceConfigFromDoc(t, e, name, svc)
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
				checkVocabulary(t, e, name, svc, schema)
			})
		}
	}

	require.NotZero(t, checked, "no azure.ai.agent doc examples were found — is the extractor still working?")
}

// checkVocabulary asserts that every property the snippet declares is one azd
// understands: a key azd core parses itself, or a property the extension's
// schema declares — recursively, so neither an unsupported sibling of `config`
// in the deprecated shape nor an unsupported key nested inside a documented
// object slips through.
func checkVocabulary(t *testing.T, e docExample, name string, svc map[string]any, schema *docSchema) {
	t.Helper()

	for key, value := range svc {
		// Core owns the shape of its own keys; coreServiceFields already
		// validated them.
		if slices.Contains(coreServiceKeys, key) {
			continue
		}

		prop, ok := schema.property(key)
		require.True(t, ok, undeclaredPropertyMessage(e, name, key))
		schema.checkValue(t, e, name, key, prop, value)
	}

	// The deprecated shape nests the agent definition under `config`, which core
	// passes through untyped — so the extension's schema, not core, owns every
	// key inside it.
	if cfg, ok := svc["config"].(map[string]any); ok {
		for key, value := range cfg {
			path := "config." + key

			prop, ok := schema.property(key)
			require.True(t, ok, undeclaredPropertyMessage(e, name, path))
			schema.checkValue(t, e, name, path, prop, value)
		}
	}
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
