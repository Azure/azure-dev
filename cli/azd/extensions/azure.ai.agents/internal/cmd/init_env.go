// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

var azureYamlEnvironmentReferencePaths = map[string][][]string{
	"azure.ai.agent": {
		{"environmentVariables", "*", "value"},
		{"config", "environmentVariables", "*", "value"},
	},
	"azure.ai.connection": {
		{"target"},
		{"credentials"},
		{"metadata"},
	},
	"azure.ai.project": {
		{"network", "agentSubnet", "vnet"},
		{"network", "peSubnet", "vnet"},
		{"network", "dns", "subscription"},
	},
	"azure.ai.routine": {
		{"action", "input"},
		{"config", "action", "input"},
	},
	"azure.ai.toolbox": {
		{"endpoint"},
		{"tools"},
		{"config", "endpoint"},
		{"config", "tools"},
	},
	"microsoft.foundry": {
		{"network", "agentSubnet", "vnet"},
		{"network", "peSubnet", "vnet"},
		{"network", "dns", "subscription"},
	},
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
		referencePaths, ok := azureYamlEnvironmentReferencePaths[host]
		if !ok {
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

		for _, referencePath := range referencePaths {
			collectAzureYamlEnvironmentReferencesAtPath(
				&resolvedService,
				referencePath,
				[]string{"services", serviceName},
				&references,
				indexByName,
			)
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

func collectAzureYamlEnvironmentReferencesAtPath(
	node *yaml.Node,
	referencePath []string,
	path []string,
	references *[]azureYamlEnvironmentReference,
	indexByName map[string]int,
) {
	if node == nil {
		return
	}
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	if len(referencePath) == 0 {
		collectAzureYamlEnvironmentReferences(node, path, references, indexByName)
		return
	}

	segment := referencePath[0]
	if segment == "*" {
		if node.Kind != yaml.SequenceNode {
			return
		}
		for _, child := range node.Content {
			collectAzureYamlEnvironmentReferencesAtPath(
				child,
				referencePath[1:],
				append(slices.Clone(path), segment),
				references,
				indexByName,
			)
		}
		return
	}

	child := yamlMappingValue(node, segment)
	if child == nil {
		return
	}
	collectAzureYamlEnvironmentReferencesAtPath(
		child,
		referencePath[1:],
		append(slices.Clone(path), segment),
		references,
		indexByName,
	)
}

func collectAzureYamlEnvironmentReferences(
	node *yaml.Node,
	path []string,
	references *[]azureYamlEnvironmentReference,
	indexByName map[string]int,
) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			collectAzureYamlEnvironmentReferences(child, path, references, indexByName)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			childPath := append(slices.Clone(path), key.Value)
			collectAzureYamlEnvironmentReferences(value, childPath, references, indexByName)
		}
	case yaml.AliasNode:
		collectAzureYamlEnvironmentReferences(node.Alias, path, references, indexByName)
	case yaml.ScalarNode:
		value := foundryTemplateSpanPattern.ReplaceAllStringFunc(node.Value, func(span string) string {
			return strings.Repeat(" ", len(span))
		})
		for _, match := range azureYamlEnvRefPattern.FindAllStringSubmatchIndex(value, -1) {
			if isEscapedAzureYamlEnvironmentReference(value, match[0]) {
				continue
			}
			if match[4] != -1 {
				continue
			}

			name := value[match[2]:match[3]]
			secret := isSecretAzureYamlEnvironmentReference(path)
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
}

func isEscapedAzureYamlEnvironmentReference(value string, start int) bool {
	precedingDollars := 0
	for i := start - 1; i >= 0 && value[i] == '$'; i-- {
		precedingDollars++
	}
	return precedingDollars%2 == 1
}

// Secret masking is based on explicit configuration structure. Environment
// variable names are user-defined and are not a reliable sensitivity signal.
func isSecretAzureYamlEnvironmentReference(path []string) bool {
	for _, segment := range path {
		switch strings.ToLower(segment) {
		case "credential", "credentials", "secret", "secrets":
			return true
		}
	}

	return false
}
