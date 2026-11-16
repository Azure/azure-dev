// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Our generated main.parameters.jsont uses ${AZURE_PRINCIPAL_ID} and ${AZURE_PRINCIPAL_TYPE},
// following core azd's Bicep convention. These placeholders need the signed-in principal
// resolved by the provider, not authored environment values.
const (
	principalIDPlaceholder   = "AZURE_PRINCIPAL_ID"
	principalTypePlaceholder = "AZURE_PRINCIPAL_TYPE"
)

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?=)([^}]*))?\}`)

type parameterDefinition struct {
	Type         string          `json:"type"`
	DefaultValue json.RawMessage `json:"defaultValue"`
	Metadata     struct {
		Description string `json:"description"`
	} `json:"metadata"`
}

// Parameters resolves generated substitutions and preserves secure-parameter metadata.
func (p *Provider) Parameters(ctx context.Context) ([]*azdext.ProvisioningParameter, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.prepare(ctx); err != nil {
		return nil, err
	}
	result := make([]*azdext.ProvisioningParameter, len(p.metadata))
	for i, parameter := range p.metadata {
		result[i] = &azdext.ProvisioningParameter{
			Name: parameter.Name, Value: parameter.Value, Secret: parameter.Secret,
			EnvVarMapping: slices.Clone(parameter.EnvVarMapping),
			LocalPrompt:   parameter.LocalPrompt, UsingEnvVarMapping: parameter.UsingEnvVarMapping,
		}
	}
	return result, nil
}

func (p *Provider) prepare(ctx context.Context) error {
	if err := p.ensureEnv(ctx); err != nil {
		return err
	}
	if err := p.build(ctx); err != nil {
		return err
	}
	if p.params != nil {
		return nil
	}
	// Scope is resolved during serial initialization, before hooks and dependent layers update inputs.
	values, err := p.loadEnvironmentValues(ctx)
	if err != nil {
		return err
	}
	values[principalIDPlaceholder] = p.scope.principalID
	values[principalTypePlaceholder] = p.scope.principalType

	parameters, definitions, err := p.loadParameterInputs()
	if err != nil {
		return err
	}

	params := map[string]any{}
	metadata := make([]*azdext.ProvisioningParameter, 0, len(definitions))
	for _, name := range slices.Sorted(maps.Keys(definitions)) {
		parameter, info, err := p.prepareParameter(ctx, name, parameters[name], definitions[name], values)
		if err != nil {
			return err
		}
		if info == nil {
			continue
		}
		params[name] = parameter
		metadata = append(metadata, info)
	}
	p.params, p.metadata = params, metadata
	return nil
}

func (p *Provider) loadParameterInputs() (map[string]map[string]any, map[string]parameterDefinition, error) {
	data, err := os.ReadFile(filepath.Join(p.path, p.module+".parameters.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("reading generated parameters: %w", err)
	}
	var file struct {
		Parameters map[string]map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, nil, fmt.Errorf("decoding generated parameters: %w", err)
	}
	definitionsJSON, err := json.Marshal(p.template["parameters"])
	if err != nil {
		return nil, nil, fmt.Errorf("encoding parameter definitions: %w", err)
	}
	definitions := map[string]parameterDefinition{}
	if err := json.Unmarshal(definitionsJSON, &definitions); err != nil {
		return nil, nil, fmt.Errorf("decoding parameter definitions: %w", err)
	}
	for name := range file.Parameters {
		if _, exists := definitions[name]; !exists {
			return nil, nil, fmt.Errorf("generated parameter %q is not declared in the Bicep template", name)
		}
	}
	return file.Parameters, definitions, nil
}

// prepareParameter returns a deployment parameter containing either a value or a Key Vault reference,
// metadata describing its value, secrecy, environment mappings, and prompting, or an error if preparation fails.
//
// Both the deployment parameter and metadata are nil, with no error, when the template default should be used.
func (p *Provider) prepareParameter(
	ctx context.Context, name string, parameter map[string]any, definition parameterDefinition, values map[string]string,
) (map[string]any, *azdext.ProvisioningParameter, error) {
	mapping := []string{}
	prompted := false
	var deploymentParameter map[string]any
	var err error

	if _, hasReference := parameter["reference"]; hasReference {
		deploymentParameter, err = p.prepareKeyVaultReferenceParameter(ctx, name, parameter, values, &mapping)
	} else {
		deploymentParameter, prompted, err = p.prepareValueParameter(
			ctx, name, parameter["value"], definition, values, &mapping)
	}

	if err != nil {
		return nil, nil, err
	}

	// Omit the parameter so the template default applies.
	if deploymentParameter == nil {
		return nil, nil, nil
	}

	// References carry no local value; their metadata must not expose the referenced secret.
	encoded, err := provisioningValue(deploymentParameter["value"])
	if err != nil {
		return nil, nil, fmt.Errorf("encoding parameter %q: %w", name, err)
	}

	slices.Sort(mapping)
	mapping = slices.Compact(mapping)

	return deploymentParameter, &azdext.ProvisioningParameter{
		Name: name, Secret: strings.HasPrefix(strings.ToLower(definition.Type), "secure"),
		Value: encoded, EnvVarMapping: mapping,
		UsingEnvVarMapping: len(mapping) != 0, LocalPrompt: prompted,
	}, nil
}

// prepareKeyVaultReferenceParameter prepares a parameter that identifies a Key Vault secret
// instead of supplying its value.
func (p *Provider) prepareKeyVaultReferenceParameter(
	ctx context.Context, name string, parameter map[string]any, values map[string]string, mapping *[]string,
) (map[string]any, error) {
	if _, hasValue := parameter["value"]; hasValue {
		return nil, fmt.Errorf("parameter %q cannot contain both value and reference", name)
	}

	resolved, err := p.resolve(ctx, parameter["reference"], values, mapping)
	if err != nil {
		return nil, fmt.Errorf("resolving parameter %q: %w", name, err)
	}

	return map[string]any{"reference": resolved}, nil
}

// prepareValueParameter prepares a parameter whose value is supplied directly.
// It returns the deployment parameter, whether its value required a local prompt, and an error if preparation fails.
// A nil deployment parameter with no error means the template default should be used.
func (p *Provider) prepareValueParameter(
	ctx context.Context, name string, input any, definition parameterDefinition, values map[string]string, mapping *[]string,
) (map[string]any, bool, error) {
	value, prompted, err := p.resolveParameterValue(ctx, name, input, values, mapping)

	if err != nil {
		return nil, false, fmt.Errorf("resolving parameter %q: %w", name, err)
	}

	if value == nil || value == "" && len(*mapping) != 0 {
		if len(definition.DefaultValue) != 0 {
			return nil, false, nil
		}
		value, err = p.promptParameter(ctx, name, definition)
		if err != nil {
			return nil, false, err
		}
		prompted = true
	}
	value, err = coerce(value, definition.Type)
	if err != nil {
		return nil, false, fmt.Errorf("parameter %q must have type %s", name, definition.Type)
	}
	return map[string]any{"value": value}, prompted, nil
}

func (p *Provider) resolveParameterValue(
	ctx context.Context, name string, value any, values map[string]string, mapping *[]string,
) (any, bool, error) {
	if p.resolver != nil {
		resolved, prompted, err := p.resolver.ResolveParameter(ctx, name, values, mapping)
		if err != nil {
			return nil, prompted, err
		}
		if resolved != nil {
			return resolved, prompted, err
		}
	}
	resolved, err := p.resolve(ctx, value, values, mapping)
	return resolved, false, err
}

func (p *Provider) promptParameter(ctx context.Context, name string, definition parameterDefinition) (any, error) {
	response, err := p.client.Prompt().Prompt(ctx, &azdext.PromptRequest{Options: &azdext.PromptOptions{
		Message: "Enter a value for " + name, HelpMessage: definition.Metadata.Description,
		Required: true, Secret: strings.HasPrefix(strings.ToLower(definition.Type), "secure"),
	}})
	if err != nil {
		return nil, fmt.Errorf("prompting for parameter %q: %w", name, err)
	}
	return response.Value, nil
}

func (p *Provider) resolve(ctx context.Context, value any, values map[string]string, mapping *[]string) (any, error) {
	switch value := value.(type) {
	case string:
		replaced := envPattern.ReplaceAllStringFunc(value, func(match string) string {
			parts := envPattern.FindStringSubmatch(match)
			if parts[1] != principalIDPlaceholder && parts[1] != principalTypePlaceholder {
				*mapping = append(*mapping, parts[1])
			}
			if resolved, exists := values[parts[1]]; exists && (resolved != "" || parts[2] != ":=") {
				return resolved
			}
			return parts[3]
		})
		if strings.Contains(replaced, "$(") {
			return nil, errors.New("command substitutions are not supported in generated parameters")
		}
		return replaced, nil
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			resolved, err := p.resolve(ctx, item, values, mapping)
			if err != nil {
				return nil, err
			}
			result[i] = resolved
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			resolved, err := p.resolve(ctx, item, values, mapping)
			if err != nil {
				return nil, err
			}
			result[key] = resolved
		}
		return result, nil
	default:
		return value, nil
	}
}

func coerce(value any, kind string) (any, error) {
	if text, ok := value.(string); ok {
		switch strings.ToLower(kind) {
		case "bool", "int", "array", "object", "secureobject":
			if err := json.Unmarshal([]byte(text), &value); err != nil {
				return nil, err
			}
		}
	}
	valid := false
	switch strings.ToLower(kind) {
	case "string", "securestring":
		_, valid = value.(string)
	case "bool":
		_, valid = value.(bool)
	case "int":
		number, ok := value.(float64)
		valid = ok && number == float64(int64(number))
	case "array":
		_, valid = value.([]any)
	case "object", "secureobject":
		_, valid = value.(map[string]any)
	}
	if !valid {
		return nil, errors.New("incompatible parameter type")
	}
	return value, nil
}
