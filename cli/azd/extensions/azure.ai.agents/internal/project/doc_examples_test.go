// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

// This file validates the `azure.yaml` examples embedded in this extension's
// markdown docs. Doc snippets used to drift from the code with nothing in CI to
// catch it: the README's migration example once omitted the required agent
// `name`, so copying it failed with "name cannot be empty". See issue #9330.
//
// Three checks run against every fenced YAML block that declares an
// `azure.ai.agent` service:
//
//  1. Resolver — the snippet must survive [AgentDefinitionFromService], the same
//     entry point azd uses at deploy time.
//  2. JSON Schema — extension-owned values must satisfy required, type, enum,
//     pattern, and additionalProperties constraints.
//  3. Core YAML — core-owned values must have the same shapes azd core parses.
//
// The vocabulary part of check 2 is intentionally stricter than the schema's
// top-level additionalProperties setting. azd ignores unrecognized service
// properties at runtime (deliberately, for forward compatibility), which is how
// a doc can advertise a setting that silently does nothing. Our own docs should
// not rely on that leniency.

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
	root     map[string]any
	compiled *jsonschema.Schema
}

// loadDocSchema reads schemas/azure.ai.agent.json from the extension root.
func loadDocSchema(t *testing.T, root string) *docSchema {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(root, "schemas", "azure.ai.agent.json"))
	require.NoError(t, err)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))

	const resourceURI = "mem://azure.ai.agent.json"
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(resourceURI, schema))

	compiled, err := compiler.Compile(resourceURI)
	require.NoError(t, err)

	s := &docSchema{
		root:     schema,
		compiled: compiled,
	}
	require.NotEmpty(t, s.properties(schema), "azure.ai.agent.json declares no properties")
	return s
}

// validate applies every JSON Schema constraint, including required fields,
// types, enums, patterns, and additionalProperties.
func (s *docSchema) validate(value map[string]any) error {
	return s.compiled.Validate(value)
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

// coreDockerFields mirrors project.DockerProjectOptions without importing the
// full project package into this test-only validator.
type coreDockerFields struct {
	Path        string   `yaml:"path"`
	Context     string   `yaml:"context"`
	Platform    string   `yaml:"platform"`
	Target      string   `yaml:"target"`
	Registry    string   `yaml:"registry"`
	Image       string   `yaml:"image"`
	Tag         string   `yaml:"tag"`
	RemoteBuild bool     `yaml:"remoteBuild"`
	Network     string   `yaml:"network"`
	BuildArgs   []string `yaml:"buildArgs"`
}

// coreK8sFields mirrors the azure.yaml-facing portion of project.AksOptions.
type coreK8sFields struct {
	Namespace      string                  `yaml:"namespace"`
	DeploymentPath string                  `yaml:"deploymentPath"`
	Ingress        coreK8sIngressFields    `yaml:"ingress"`
	Deployment     coreK8sDeploymentFields `yaml:"deployment"`
	Service        coreK8sServiceFields    `yaml:"service"`
	Helm           *coreHelmFields         `yaml:"helm"`
	Kustomize      *coreKustomizeFields    `yaml:"kustomize"`
}

type coreK8sIngressFields struct {
	Name         string `yaml:"name"`
	RelativePath string `yaml:"relativePath"`
}

type coreK8sDeploymentFields struct {
	Name string `yaml:"name"`
}

type coreK8sServiceFields struct {
	Name string `yaml:"name"`
}

type coreHelmFields struct {
	Repositories []*coreHelmRepositoryFields `yaml:"repositories"`
	Releases     []*coreHelmReleaseFields    `yaml:"releases"`
}

type coreHelmRepositoryFields struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

type coreHelmReleaseFields struct {
	Name      string `yaml:"name"`
	Chart     string `yaml:"chart"`
	Version   string `yaml:"version"`
	Namespace string `yaml:"namespace"`
	Values    string `yaml:"values"`
}

type coreKustomizeFields struct {
	Directory string            `yaml:"dir"`
	Edits     []string          `yaml:"edits"`
	Env       map[string]string `yaml:"env"`
}

type coreDeploymentStacksFields struct {
	ActionOnUnmanage *coreActionOnUnmanageFields `yaml:"actionOnUnmanage"`
	DenySettings     *coreDenySettingsFields     `yaml:"denySettings"`
}

type coreActionOnUnmanageFields struct {
	Resources        string `yaml:"resources"`
	ResourceGroups   string `yaml:"resourceGroups"`
	ManagementGroups string `yaml:"managementGroups"`
}

type coreDenySettingsFields struct {
	Mode               string   `yaml:"mode"`
	ApplyToChildScopes *bool    `yaml:"applyToChildScopes"`
	ExcludedActions    []string `yaml:"excludedActions"`
	ExcludedPrincipals []string `yaml:"excludedPrincipals"`
}

// coreHookFields mirrors the YAML-facing fields of ext.HookConfig.
type coreHookFields struct {
	Name            string            `yaml:",omitempty"`
	Kind            string            `yaml:"kind"`
	Shell           string            `yaml:"shell"`
	Dir             string            `yaml:"dir"`
	Run             string            `yaml:"run"`
	ContinueOnError bool              `yaml:"continueOnError"`
	Interactive     bool              `yaml:"interactive"`
	Windows         *coreHookFields   `yaml:"windows"`
	Posix           *coreHookFields   `yaml:"posix"`
	Secrets         map[string]string `yaml:"secrets"`
	Config          map[string]any    `yaml:"config"`
}

// coreHooksFields mirrors ext.HooksConfig.UnmarshalYAML: each hook is either a
// mapping or a sequence of mappings. Scalars are rejected instead of being
// silently accepted by a map[string]any placeholder.
type coreHooksFields map[string][]*coreHookFields

// strictYAMLUnmarshal is deliberately stricter than core's forward-compatible
// decoder. Runtime must allow future extension fields, but this extension's own
// docs must not publish a misspelled core field that azd silently ignores.
func strictYAMLUnmarshal(data []byte, value any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(value)
}

func (h *coreHooksFields) UnmarshalYAML(unmarshal func(any) error) error {
	var raw map[string]any
	if err := unmarshal(&raw); err != nil {
		return err
	}

	result := make(coreHooksFields, len(raw))
	for name, value := range raw {
		switch value.(type) {
		case nil:
			result[name] = []*coreHookFields{nil}
		case map[string]any:
			encoded, err := yaml.Marshal(value)
			if err != nil {
				return err
			}
			var hook coreHookFields
			if err := strictYAMLUnmarshal(encoded, &hook); err != nil {
				return fmt.Errorf("failed to unmarshal hook %q: %w", name, err)
			}
			result[name] = []*coreHookFields{&hook}
		case []any:
			encoded, err := yaml.Marshal(value)
			if err != nil {
				return err
			}
			var hooks []*coreHookFields
			if err := strictYAMLUnmarshal(encoded, &hooks); err != nil {
				return fmt.Errorf("failed to unmarshal hook %q: %w", name, err)
			}
			result[name] = hooks
		default:
			return fmt.Errorf(
				"failed to unmarshal hook %q: expected mapping or sequence, got %T",
				name, value,
			)
		}
	}

	*h = result
	return nil
}

func validateCoreHooks(hooks coreHooksFields) error {
	for name, list := range hooks {
		if list == nil {
			return fmt.Errorf(
				"hook %q has an empty definition; expected properties such as run or shell",
				name,
			)
		}
		for i, hook := range list {
			if hook == nil {
				return fmt.Errorf(
					"hook %q entry %d has an empty definition; expected properties such as run or shell",
					name, i+1,
				)
			}
		}
	}
	return nil
}

// coreInfraFields mirrors the azure.yaml-facing portion of
// provisioning.Options.
type coreInfraFields struct {
	Provider         string                      `yaml:"provider"`
	Path             string                      `yaml:"path"`
	Module           string                      `yaml:"module"`
	Name             string                      `yaml:"name"`
	Hooks            coreHooksFields             `yaml:"hooks"`
	DeploymentStacks *coreDeploymentStacksFields `yaml:"deploymentStacks"`
	Config           map[string]any              `yaml:"config"`
	DependsOn        []string                    `yaml:"dependsOn"`
	Layers           []coreInfraFields           `yaml:"layers"`
}

func validateCoreInfraFields(infra coreInfraFields) error {
	if err := validateCoreHooks(infra.Hooks); err != nil {
		return fmt.Errorf("infra: %w", err)
	}
	for i, layer := range infra.Layers {
		if err := validateCoreInfraFields(layer); err != nil {
			return fmt.Errorf("infra.layers[%d]: %w", i, err)
		}
	}
	return nil
}

// coreServiceFields mirrors the scalar fields azd core parses out of a
// `services.<name>` block, following ServiceConfig in
// cli/azd/pkg/project/service_config.go. Complex core-owned fields mirror their
// exact YAML shapes and unmarshalling rules — for example, a scalar hook value
// must fail exactly as HooksConfig rejects it at runtime.
//
// Everything core does not name lands in AdditionalProperties, exactly as
// core's `yaml:",inline"` field does before the block reaches this extension.
type coreServiceFields struct {
	ResourceGroupName    string            `yaml:"resourceGroup"`
	ResourceName         string            `yaml:"resourceName"`
	ApiVersion           string            `yaml:"apiVersion"`
	RelativePath         string            `yaml:"project"`
	Host                 string            `yaml:"host"`
	Language             string            `yaml:"language"`
	OutputPath           string            `yaml:"dist"`
	Image                string            `yaml:"image"`
	Docker               coreDockerFields  `yaml:"docker"`
	K8s                  coreK8sFields     `yaml:"k8s"`
	Module               string            `yaml:"module"`
	Infra                coreInfraFields   `yaml:"infra"`
	Hooks                coreHooksFields   `yaml:"hooks"`
	Uses                 []string          `yaml:"uses"`
	Config               map[string]any    `yaml:"config"`
	Environment          map[string]string `yaml:"env"`
	Condition            string            `yaml:"condition"`
	RemoteBuild          *bool             `yaml:"remoteBuild"`
	AdditionalProperties map[string]any    `yaml:",inline"`
}

// decodeCoreServiceFields applies the YAML types and custom unmarshallers azd
// core uses for its part of a service block.
func decodeCoreServiceFields(svc map[string]any) (coreServiceFields, error) {
	raw, err := yaml.Marshal(svc)
	if err != nil {
		return coreServiceFields{}, err
	}

	var core coreServiceFields
	if err := strictYAMLUnmarshal(raw, &core); err != nil {
		return coreServiceFields{}, err
	}
	if err := validateCoreHooks(core.Hooks); err != nil {
		return coreServiceFields{}, err
	}
	if err := validateCoreInfraFields(core.Infra); err != nil {
		return coreServiceFields{}, err
	}
	return core, nil
}

// serviceConfigFromDoc builds the ServiceConfig azd core would hand this
// extension for the given service block, failing the test if any field core
// itself parses has a shape core would reject.
func serviceConfigFromDoc(t *testing.T, e docExample, name string, svc map[string]any) *azdext.ServiceConfig {
	t.Helper()

	core, err := decodeCoreServiceFields(svc)
	require.NoError(t, err,
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

func TestDecodeCoreServiceFieldsRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{
			name: "docker must be an object",
			content: `host: azure.ai.agent
docker: invalid`,
		},
		{
			name: "docker nested fields keep their core types",
			content: `host: azure.ai.agent
docker:
  remoteBuild: sometimes`,
		},
		{
			name: "docker rejects unknown fields",
			content: `host: azure.ai.agent
docker:
  buildArgz: [FOO=bar]`,
		},
		{
			name: "k8s must be an object",
			content: `host: azure.ai.agent
k8s: invalid`,
		},
		{
			name: "k8s nested fields keep their core types",
			content: `host: azure.ai.agent
k8s:
  helm:
    repositories: invalid`,
		},
		{
			name: "k8s rejects unknown fields",
			content: `host: azure.ai.agent
k8s:
  namespaze: default`,
		},
		{
			name: "infra must be an object",
			content: `host: azure.ai.agent
infra: invalid`,
		},
		{
			name: "infra nested fields keep their core types",
			content: `host: azure.ai.agent
infra:
  layers: invalid`,
		},
		{
			name: "infra rejects unknown fields",
			content: `host: azure.ai.agent
infra:
  providerz: bicep`,
		},
		{
			name: "deployment stacks keep their core types",
			content: `host: azure.ai.agent
infra:
  deploymentStacks:
    denySettings: invalid`,
		},
		{
			name: "deployment stacks reject unknown fields",
			content: `host: azure.ai.agent
infra:
  deploymentStacks:
    denySettingz:
      mode: none`,
		},
		{
			name: "hook must be an object or list",
			content: `host: azure.ai.agent
hooks:
  preprovision: 1`,
		},
		{
			name: "hook fields keep their core types",
			content: `host: azure.ai.agent
hooks:
  preprovision:
    run: [not, a, string]`,
		},
		{
			name: "hook rejects unknown fields",
			content: `host: azure.ai.agent
hooks:
  preprovision:
    continueOnErrror: true`,
		},
		{
			name: "empty hook definition",
			content: `host: azure.ai.agent
hooks:
  preprovision:`,
		},
		{
			name: "empty hook list entry",
			content: `host: azure.ai.agent
hooks:
  preprovision:
    - null`,
		},
		{
			name: "empty infra hook definition",
			content: `host: azure.ai.agent
infra:
  hooks:
    preprovision:`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var svc map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(test.content), &svc))

			_, err := decodeCoreServiceFields(svc)
			require.Error(t, err)
		})
	}
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

func TestDocSchemaValidatesConstraints(t *testing.T) {
	t.Parallel()

	schema := loadDocSchema(t, extensionRoot(t))
	type fixture struct {
		value      map[string]any
		deployment map[string]any
		sku        map[string]any
	}
	validDeployment := func() fixture {
		sku := map[string]any{
			"name":     "GlobalStandard",
			"capacity": 10,
		}
		deployment := map[string]any{
			"name": "gpt-4o",
			"model": map[string]any{
				"name":    "gpt-4o",
				"format":  "OpenAI",
				"version": "2024-08-06",
			},
			"sku": sku,
		}
		return fixture{
			value: map[string]any{
				"deployments": []any{
					deployment,
				},
			},
			deployment: deployment,
			sku:        sku,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*fixture)
		wantErr bool
	}{
		{name: "valid deployment"},
		{
			name: "required model",
			mutate: func(value *fixture) {
				delete(value.deployment, "model")
			},
			wantErr: true,
		},
		{
			name: "required sku",
			mutate: func(value *fixture) {
				delete(value.deployment, "sku")
			},
			wantErr: true,
		},
		{
			name: "capacity type",
			mutate: func(value *fixture) {
				value.sku["capacity"] = "ten"
			},
			wantErr: true,
		},
		{
			name: "kind enum",
			mutate: func(value *fixture) {
				value.value["kind"] = "serverless"
			},
			wantErr: true,
		},
		{
			name: "connection name pattern",
			mutate: func(value *fixture) {
				value.value["connections"] = []any{
					map[string]any{
						"name":     "!",
						"category": "CustomKeys",
						"target":   "https://example.test",
						"authType": "None",
					},
				}
			},
			wantErr: true,
		},
		{
			name: "session idle timeout min valid",
			mutate: func(value *fixture) {
				value.value["sessionConfiguration"] = map[string]any{"idleTimeoutSeconds": 300}
			},
		},
		{
			name: "session idle timeout max valid",
			mutate: func(value *fixture) {
				value.value["sessionConfiguration"] = map[string]any{"idleTimeoutSeconds": 3600}
			},
		},
		{
			name: "session idle timeout below min",
			mutate: func(value *fixture) {
				value.value["sessionConfiguration"] = map[string]any{"idleTimeoutSeconds": 299}
			},
			wantErr: true,
		},
		{
			name: "session idle timeout above max",
			mutate: func(value *fixture) {
				value.value["sessionConfiguration"] = map[string]any{"idleTimeoutSeconds": 3601}
			},
			wantErr: true,
		},
		{
			name: "session idle timeout unknown property",
			mutate: func(value *fixture) {
				value.value["sessionConfiguration"] = map[string]any{"idleTimeoutMinutes": 5}
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			value := validDeployment()
			if test.mutate != nil {
				test.mutate(&value)
			}

			err := schema.validate(value.value)
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestActiveDocAgentConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		inline       map[string]any
		config       map[string]any
		want         map[string]any
		wantLocation string
		wantErr      string
	}{
		{
			name:         "inline only",
			inline:       map[string]any{"kind": "hosted", "name": "agent"},
			want:         map[string]any{"kind": "hosted", "name": "agent"},
			wantLocation: "inline",
		},
		{
			name:         "config only",
			config:       map[string]any{"kind": "hosted", "name": "agent"},
			want:         map[string]any{"kind": "hosted", "name": "agent"},
			wantLocation: "config",
		},
		{
			name:         "no extension properties",
			want:         map[string]any{},
			wantLocation: "inline",
		},
		{
			name:    "inline definition makes config inactive",
			inline:  map[string]any{"kind": "hosted", "name": "agent"},
			config:  map[string]any{"container": map[string]any{}},
			wantErr: "deprecated config properties are ignored",
		},
		{
			name:    "config definition makes inline inactive",
			inline:  map[string]any{"container": map[string]any{}},
			config:  map[string]any{"kind": "hosted", "name": "agent"},
			wantErr: "inline properties are ignored",
		},
		{
			name:    "inline properties win when neither location has kind",
			inline:  map[string]any{"container": map[string]any{}},
			config:  map[string]any{"deployments": []any{}},
			wantErr: "deprecated config properties are ignored",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, location, err := activeDocAgentConfig(test.inline, test.config)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, test.want, got)
			require.Equal(t, test.wantLocation, location)
		})
	}
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

	inline := map[string]any{}
	for key, value := range svc {
		// Core owns the shape of its own keys; coreServiceFields already
		// validated them.
		if slices.Contains(coreServiceKeys, key) {
			continue
		}

		prop, ok := schema.property(key)
		require.True(t, ok, undeclaredPropertyMessage(e, name, key))
		schema.checkValue(t, e, name, key, prop, value)

		// `$ref` points at the file carrying the definition; it is not part of
		// the definition itself, so it neither makes the inline shape active nor
		// conflicts with a deprecated config block.
		if key == AgentDefinitionRefKey {
			continue
		}
		inline[key] = value
	}

	// The deprecated shape nests the agent definition under `config`, which core
	// passes through untyped — so the extension's schema, not core, owns every
	// key inside it.
	cfg, _ := svc["config"].(map[string]any)
	if cfg != nil {
		for key, value := range cfg {
			path := "config." + key

			prop, ok := schema.property(key)
			require.True(t, ok, undeclaredPropertyMessage(e, name, path))
			schema.checkValue(t, e, name, path, prop, value)
		}
	}

	active, location, err := activeDocAgentConfig(inline, cfg)
	require.NoError(t, err, "%s: service %q contains ignored agent properties", e, name)
	require.NoError(t, schema.validate(active),
		"%s: service %q %s properties do not satisfy schemas/azure.ai.agent.json. "+
			"Fix the example so it can be copied into azure.yaml as-is.", e, name, location)
}

// activeDocAgentConfig mirrors ServiceConfigProps: inline extension properties
// win unless they omit kind and the deprecated config block declares it. Since
// the locations are never merged, documenting both would make one silently
// ineffective and is therefore rejected.
func activeDocAgentConfig(inline, config map[string]any) (map[string]any, string, error) {
	if len(inline) == 0 && len(config) == 0 {
		return map[string]any{}, "inline", nil
	}
	if len(inline) == 0 {
		return config, "config", nil
	}
	if len(config) == 0 {
		return inline, "inline", nil
	}

	inlineKind, _ := inline["kind"].(string)
	configKind, _ := config["kind"].(string)
	if inlineKind == "" && configKind != "" {
		return nil, "", fmt.Errorf(
			"inline properties are ignored because deprecated config declares the active agent definition; " +
				"move them under config or migrate the whole definition inline",
		)
	}
	return nil, "", fmt.Errorf(
		"deprecated config properties are ignored because the inline agent definition is active; " +
			"remove config or migrate all of its properties inline",
	)
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
