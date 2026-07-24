// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"gopkg.in/yaml.v3"
)

// azureYamlEnvRefPattern matches bare ${VAR} references and references with a
// fallback. Group 2 is non-empty for ${VAR:-default}, which does not require an
// environment value because the runtime expander supplies the fallback.
var azureYamlEnvRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

var foundryTemplateSpanPattern = regexp.MustCompile(`(?s)\$\{\{.*?\}\}`)

// These types mirror only the fields each Foundry provider expands from the
// azd environment. The owning provider types are unexported or live in sibling
// extension modules, so init keeps small typed views instead of string paths.
//
// Keep these views aligned with:
//   - agent environmentVariables values
//   - connection target, credentials, and metadata
//   - project network VNet IDs and DNS subscription
//   - routine action input
//   - toolbox endpoint and tools
type azureYamlAgentEnvironmentConfig struct {
	Kind                 string                         `yaml:"kind,omitempty"`
	EnvironmentVariables []azureYamlEnvironmentVariable `yaml:"environmentVariables,omitempty"`
}

type azureYamlEnvironmentVariable struct {
	Value string `yaml:"value,omitempty"`
}

type azureYamlConnectionEnvironmentConfig struct {
	Target      string            `yaml:"target,omitempty"`
	Credentials map[string]any    `yaml:"credentials,omitempty"`
	Metadata    map[string]string `yaml:"metadata,omitempty"`
}

type azureYamlProjectEnvironmentConfig struct {
	Network *azureYamlNetworkEnvironmentConfig `yaml:"network,omitempty"`
}

type azureYamlNetworkEnvironmentConfig struct {
	AgentSubnet *azureYamlSubnetEnvironmentConfig `yaml:"agentSubnet,omitempty"`
	PESubnet    *azureYamlSubnetEnvironmentConfig `yaml:"peSubnet,omitempty"`
	DNS         *azureYamlDNSEnvironmentConfig    `yaml:"dns,omitempty"`
}

type azureYamlSubnetEnvironmentConfig struct {
	VNet string `yaml:"vnet,omitempty"`
}

type azureYamlDNSEnvironmentConfig struct {
	Subscription string `yaml:"subscription,omitempty"`
}

type azureYamlRoutineEnvironmentConfig struct {
	Action *azureYamlRoutineActionEnvironmentConfig `yaml:"action,omitempty"`
}

type azureYamlRoutineActionEnvironmentConfig struct {
	Input any `yaml:"input,omitempty"`
}

type azureYamlToolboxEnvironmentConfig struct {
	Endpoint string           `yaml:"endpoint,omitempty"`
	Tools    []map[string]any `yaml:"tools,omitempty"`
}

var azureYamlCoreServiceFields = map[string]struct{}{
	"apiVersion":    {},
	"condition":     {},
	"config":        {},
	"dist":          {},
	"docker":        {},
	"env":           {},
	"hooks":         {},
	"host":          {},
	"image":         {},
	"infra":         {},
	"k8s":           {},
	"language":      {},
	"module":        {},
	"project":       {},
	"remoteBuild":   {},
	"resourceGroup": {},
	"resourceName":  {},
	"uses":          {},
}

type azureYamlEnvironmentReference struct {
	Name   string
	Secret bool
}

// configureAzureYamlEnvironmentVariables prompts for unset environment values
// referenced by the adopted azure.yaml and persists them in the active azd
// environment.
func configureAzureYamlEnvironmentVariables(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	projectDir string,
	noPrompt bool,
) error {
	if noPrompt {
		return nil
	}

	manifestPath := filepath.Join(projectDir, "azure.yaml")
	//nolint:gosec // projectDir is the user-selected project root created by init
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading adopted azure.yaml: %w", err)
	}

	references, err := findAzureYamlEnvironmentReferences(content, projectDir)
	if err != nil {
		return fmt.Errorf("finding environment variable references in azure.yaml: %w", err)
	}
	if len(references) == 0 {
		return nil
	}

	envResp, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envName,
	})
	if err != nil {
		return fmt.Errorf("reading azd environment %q: %w", envName, err)
	}

	existing := make(map[string]string, len(envResp.KeyValues))
	for _, keyValue := range envResp.KeyValues {
		existing[keyValue.Key] = keyValue.Value
	}

	missing := make([]azureYamlEnvironmentReference, 0, len(references))
	for _, reference := range references {
		if value, ok := existing[reference.Name]; ok {
			if value == "" {
				missing = append(missing, reference)
			}
			continue
		}

		if value, ok := os.LookupEnv(reference.Name); ok && value != "" {
			if err := setEnvValue(ctx, azdClient, envName, reference.Name, value); err != nil {
				return err
			}
			existing[reference.Name] = value
			continue
		}

		missing = append(missing, reference)
	}
	if len(missing) == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("azure.yaml references environment variables that need to be configured:")
	fmt.Println()

	for _, reference := range missing {
		promptResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:           fmt.Sprintf("Enter a value for %s", reference.Name),
				HelpMessage:       "The value will be stored in the current azd environment.",
				Required:          true,
				RequiredMessage:   fmt.Sprintf("A value is required for %s.", reference.Name),
				IgnoreHintKeys:    true,
				Secret:            reference.Secret,
				ClearOnCompletion: reference.Secret,
			},
		})
		if err != nil {
			return exterrors.FromPrompt(
				err,
				fmt.Sprintf("failed to prompt for environment variable %s", reference.Name),
			)
		}
		if promptResp == nil || promptResp.Value == "" {
			return fmt.Errorf("no value provided for environment variable %s", reference.Name)
		}

		if err := setEnvValue(ctx, azdClient, envName, reference.Name, promptResp.Value); err != nil {
			return err
		}
	}

	return nil
}

func findAzureYamlEnvironmentReferences(content []byte, projectDir string) ([]azureYamlEnvironmentReference, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("parsing azure.yaml: %w", err)
	}

	if len(document.Content) == 0 {
		return nil, nil
	}

	services := yamlMappingValue(document.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, nil
	}

	var references []azureYamlEnvironmentReference
	indexByName := make(map[string]int)
	for i := 0; i+1 < len(services.Content); i += 2 {
		serviceName := services.Content[i].Value
		service := services.Content[i+1]
		host, ok := foundryAzureYamlServiceHost(service)
		if !ok {
			continue
		}
		if !supportsAzureYamlEnvironmentReferences(host) {
			continue
		}

		var raw map[string]any
		if err := service.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decoding service %q: %w", serviceName, err)
		}
		resolved, err := foundry.ResolveFileRefs(raw, projectDir)
		if err != nil {
			return nil, fmt.Errorf("resolving $ref includes for service %q: %w", serviceName, err)
		}
		var resolvedService yaml.Node
		if err := resolvedService.Encode(resolved); err != nil {
			return nil, fmt.Errorf("encoding resolved service %q: %w", serviceName, err)
		}

		if err := collectAzureYamlServiceEnvironmentReferences(
			host,
			serviceName,
			&resolvedService,
			&references,
			indexByName,
		); err != nil {
			return nil, err
		}
	}
	return references, nil
}

func yamlMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func foundryAzureYamlServiceHost(service *yaml.Node) (string, bool) {
	host := yamlMappingValue(service, "host")
	if host == nil || host.Kind != yaml.ScalarNode {
		return "", false
	}

	_, knownHost := foundryServiceHosts[host.Value]
	if !knownHost && !strings.HasPrefix(host.Value, "azure.ai.") {
		return "", false
	}
	return host.Value, true
}

func supportsAzureYamlEnvironmentReferences(host string) bool {
	switch host {
	case "azure.ai.agent",
		"azure.ai.connection",
		"azure.ai.project",
		"azure.ai.routine",
		"azure.ai.toolbox",
		"microsoft.foundry":
		return true
	default:
		return false
	}
}

func activeAzureYamlServiceConfiguration(host string, service *yaml.Node) *yaml.Node {
	switch host {
	case "azure.ai.agent":
		if hasNonEmptyAzureYamlString(service, "kind") {
			return service
		}
		config := yamlMappingValue(service, "config")
		if hasNonEmptyAzureYamlString(config, "kind") {
			return config
		}
		return nil
	case "azure.ai.routine", "azure.ai.toolbox":
		if hasAzureYamlInlineProperties(service) {
			return service
		}
		return yamlMappingValue(service, "config")
	default:
		return service
	}
}

func hasNonEmptyAzureYamlString(node *yaml.Node, key string) bool {
	value := yamlMappingValue(node, key)
	return value != nil && value.Kind == yaml.ScalarNode && value.Value != ""
}

func hasAzureYamlInlineProperties(service *yaml.Node) bool {
	if service == nil || service.Kind != yaml.MappingNode {
		return false
	}

	for i := 0; i+1 < len(service.Content); i += 2 {
		if _, isCoreField := azureYamlCoreServiceFields[service.Content[i].Value]; !isCoreField {
			return true
		}
	}
	return false
}

func collectAzureYamlServiceEnvironmentReferences(
	host string,
	serviceName string,
	service *yaml.Node,
	references *[]azureYamlEnvironmentReference,
	indexByName map[string]int,
) error {
	active := activeAzureYamlServiceConfiguration(host, service)
	if active == nil {
		return nil
	}

	switch host {
	case "azure.ai.agent":
		var config azureYamlAgentEnvironmentConfig
		if err := active.Decode(&config); err != nil {
			return fmt.Errorf("decoding agent service %q: %w", serviceName, err)
		}
		for _, variable := range config.EnvironmentVariables {
			collectAzureYamlEnvironmentReferences(variable.Value, false, true, references, indexByName)
		}
	case "azure.ai.connection":
		var config azureYamlConnectionEnvironmentConfig
		if err := active.Decode(&config); err != nil {
			return fmt.Errorf("decoding connection service %q: %w", serviceName, err)
		}
		collectAzureYamlEnvironmentReferences(config.Target, false, true, references, indexByName)
		if err := collectAzureYamlEnvironmentReferencesFromValue(
			config.Credentials,
			true,
			references,
			indexByName,
		); err != nil {
			return fmt.Errorf("scanning connection service %q credentials: %w", serviceName, err)
		}
		if err := collectAzureYamlEnvironmentReferencesFromValue(
			config.Metadata,
			false,
			references,
			indexByName,
		); err != nil {
			return fmt.Errorf("scanning connection service %q metadata: %w", serviceName, err)
		}
	case "azure.ai.project", "microsoft.foundry":
		var config azureYamlProjectEnvironmentConfig
		if err := active.Decode(&config); err != nil {
			return fmt.Errorf("decoding project service %q: %w", serviceName, err)
		}
		if config.Network != nil {
			if config.Network.AgentSubnet != nil {
				collectAzureYamlEnvironmentReferences(
					config.Network.AgentSubnet.VNet,
					false,
					false,
					references,
					indexByName,
				)
			}
			if config.Network.PESubnet != nil {
				collectAzureYamlEnvironmentReferences(
					config.Network.PESubnet.VNet,
					false,
					false,
					references,
					indexByName,
				)
			}
			if config.Network.DNS != nil {
				collectAzureYamlEnvironmentReferences(
					config.Network.DNS.Subscription,
					false,
					false,
					references,
					indexByName,
				)
			}
		}
	case "azure.ai.routine":
		var config azureYamlRoutineEnvironmentConfig
		if err := active.Decode(&config); err != nil {
			return fmt.Errorf("decoding routine service %q: %w", serviceName, err)
		}
		if config.Action != nil {
			if err := collectAzureYamlEnvironmentReferencesFromValue(
				config.Action.Input,
				false,
				references,
				indexByName,
			); err != nil {
				return fmt.Errorf("scanning routine service %q action input: %w", serviceName, err)
			}
		}
	case "azure.ai.toolbox":
		var config azureYamlToolboxEnvironmentConfig
		if err := active.Decode(&config); err != nil {
			return fmt.Errorf("decoding toolbox service %q: %w", serviceName, err)
		}
		collectAzureYamlEnvironmentReferences(config.Endpoint, false, true, references, indexByName)
		if err := collectAzureYamlEnvironmentReferencesFromValue(
			config.Tools,
			false,
			references,
			indexByName,
		); err != nil {
			return fmt.Errorf("scanning toolbox service %q tools: %w", serviceName, err)
		}
	}

	return nil
}

func collectAzureYamlEnvironmentReferencesFromValue(
	value any,
	secret bool,
	references *[]azureYamlEnvironmentReference,
	indexByName map[string]int,
) error {
	if value == nil {
		return nil
	}

	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return fmt.Errorf("encoding value: %w", err)
	}
	collectAzureYamlEnvironmentReferencesFromNode(&node, secret, references, indexByName)
	return nil
}

func collectAzureYamlEnvironmentReferencesFromNode(
	node *yaml.Node,
	secret bool,
	references *[]azureYamlEnvironmentReference,
	indexByName map[string]int,
) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			collectAzureYamlEnvironmentReferencesFromNode(child, secret, references, indexByName)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			childSecret := secret || isSecretAzureYamlEnvironmentKey(key.Value)
			collectAzureYamlEnvironmentReferencesFromNode(value, childSecret, references, indexByName)
		}
	case yaml.AliasNode:
		collectAzureYamlEnvironmentReferencesFromNode(node.Alias, secret, references, indexByName)
	case yaml.ScalarNode:
		collectAzureYamlEnvironmentReferences(node.Value, secret, true, references, indexByName)
	}
}

func collectAzureYamlEnvironmentReferences(
	value string,
	secret bool,
	honorEscaping bool,
	references *[]azureYamlEnvironmentReference,
	indexByName map[string]int,
) {
	value = foundryTemplateSpanPattern.ReplaceAllStringFunc(value, func(span string) string {
		return strings.Repeat(" ", len(span))
	})
	for _, match := range azureYamlEnvRefPattern.FindAllStringSubmatchIndex(value, -1) {
		if honorEscaping && isEscapedAzureYamlEnvironmentReference(value, match[0]) {
			continue
		}
		if match[4] != -1 {
			continue
		}

		name := value[match[2]:match[3]]
		if index, ok := indexByName[name]; ok {
			if secret {
				(*references)[index].Secret = true
			}
			continue
		}

		indexByName[name] = len(*references)
		*references = append(*references, azureYamlEnvironmentReference{
			Name:   name,
			Secret: secret,
		})
	}
}

func isEscapedAzureYamlEnvironmentReference(value string, start int) bool {
	precedingDollars := 0
	for i := start - 1; i >= 0 && value[i] == '$'; i-- {
		precedingDollars++
	}
	return precedingDollars%2 == 1
}

func isSecretAzureYamlEnvironmentKey(key string) bool {
	switch strings.ToLower(key) {
	case "credential", "credentials", "secret", "secrets":
		return true
	}
	return false
}
