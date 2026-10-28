// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	envKeyBYOJournal = "AZD_FOUNDRY_BYO_AKS_PREPARATION"
	byoOwnerTag      = "azd-foundry-byo-aks-deployment"
)

type byoResourceOrigin struct {
	ID              string `json:"id"`
	Source          string `json:"source"`
	InitiallyAbsent bool   `json:"initiallyAbsent"`
}

type byoState struct {
	Version        int                 `json:"version"`
	Status         string              `json:"status"`
	Plan           onboarding.Plan     `json:"plan"`
	TemplateDigest string              `json:"templateDigest"`
	BindingDigest  string              `json:"bindingDigest"`
	Origins        []byoResourceOrigin `json:"origins"`
	Facts          onboarding.Facts    `json:"facts"`
	Ownership      map[string]any      `json:"ownership,omitempty"`
}

// The journal records deployment intent; it is not a distributed lock.
type byoJournal interface {
	Load(context.Context) (*byoState, error)
	Save(context.Context, byoState) error
}

type environmentBYOJournal struct{ provider *FoundryProvisioningProvider }

func (j environmentBYOJournal) Load(ctx context.Context) (*byoState, error) {
	raw, err := j.provider.byoEnvValue(ctx, envKeyBYOJournal)
	if err != nil {
		return nil, fmt.Errorf("read BYO AKS deployment intent: %w", err)
	}
	if raw == "" {
		return nil, nil
	}
	var state byoState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, fmt.Errorf("invalid %s; retain it for manual recovery: %w", envKeyBYOJournal, err)
	}
	if state.Version != 1 || state.Plan.DeploymentID == "" || state.TemplateDigest == "" ||
		state.BindingDigest == "" || len(state.Origins) == 0 ||
		(state.Status != "deploying" && state.Status != "succeeded") {
		return nil, fmt.Errorf("unsupported or incomplete %s; retain it for manual recovery", envKeyBYOJournal)
	}
	if err := validateBYOFacts(state.Plan, state.Facts); err != nil {
		return nil, fmt.Errorf("invalid %s; retain it for manual recovery: %w", envKeyBYOJournal, err)
	}
	return &state, nil
}

func (j environmentBYOJournal) Save(ctx context.Context, state byoState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return j.provider.setEnv(ctx, envKeyBYOJournal, string(raw))
}

func (p *FoundryProvisioningProvider) ownedBYOJournal() byoJournal {
	if p.byoJournal != nil {
		return p.byoJournal
	}
	return environmentBYOJournal{p}
}

func (p *FoundryProvisioningProvider) byoEnvValue(ctx context.Context, key string) (string, error) {
	if value, present := p.virtualEnv[key]; present {
		return strings.TrimSpace(value), nil
	}
	if p.azdClient == nil {
		return "", nil
	}
	response, err := p.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{EnvName: p.envName, Key: key})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(response.GetValue()), nil
}

func (p *FoundryProvisioningProvider) guardBYOMode(ctx context.Context) error {
	if p.synthResult != nil && p.synthResult.Hosting != nil {
		return nil
	}
	state, err := p.ownedBYOJournal().Load(ctx)
	if err != nil {
		return err
	}
	if state != nil {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"a BYO AKS deployment intent remains; changing to another hosting mode is not supported",
			"retain the deployment intent and restore the original agentHosting configuration")
	}
	return nil
}

func (p *FoundryProvisioningProvider) refuseBYODestroy(ctx context.Context) error {
	hosting := p.synthResult != nil && p.synthResult.Hosting != nil
	if !hosting {
		state, err := p.ownedBYOJournal().Load(ctx)
		if err != nil {
			return err
		}
		hosting = state != nil
	}
	if hosting {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"azd down is not supported for BYO AKS deployments; no resources were deleted",
			"retain the environment deployment intent and use an explicit, resource-scoped cleanup; "+
				"do not delete the resource group containing the existing AKS or networking resources")
	}
	return nil
}

func byoParameterValue(source *templateSource, key string) any {
	if envelope, ok := source.parameters[key].(map[string]any); ok {
		return envelope["value"]
	}
	if definitions, ok := source.armTemplate["parameters"].(map[string]any); ok {
		if definition, ok := definitions[key].(map[string]any); ok {
			return definition["defaultValue"]
		}
	}
	return nil
}

func byoTemplateDigest(template map[string]any) (string, error) {
	return onboarding.Digest(byoTemplateContent(template))
}

func byoTemplateContent(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		schema, _ := value["$schema"].(string)
		isTemplate := strings.HasSuffix(strings.ToLower(schema), "deploymenttemplate.json#")
		for key, child := range value {
			if key == "metadata" && isTemplate {
				continue
			}
			if isTemplate && (key == "parameters" || key == "outputs" || key == "definitions") {
				result[key] = byoDeclarationMap(child)
			} else {
				result[key] = byoTemplateContent(child)
			}
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, child := range value {
			result[i] = byoTemplateContent(child)
		}
		return result
	default:
		return value
	}
}

func byoDeclarationMap(value any) any {
	definitions, ok := value.(map[string]any)
	if !ok {
		return value
	}
	result := make(map[string]any, len(definitions))
	for key, definition := range definitions {
		result[key] = byoDeclaration(definition)
	}
	return result
}

func byoDeclaration(value any) any {
	definition, ok := value.(map[string]any)
	if !ok {
		return value
	}
	result := make(map[string]any, len(definition))
	for key, child := range definition {
		switch key {
		case "type":
			// ARM export capitalizes declarations, not resource types or authored values.
			if kind, ok := child.(string); ok {
				switch lower := strings.ToLower(kind); lower {
				case "string", "securestring", "int", "bool", "object", "secureobject", "array":
					child = lower
				}
			}
			result[key] = child
		case "properties":
			result[key] = byoDeclarationMap(child)
		case "items", "additionalProperties":
			result[key] = byoDeclaration(child)
		default:
			result[key] = byoTemplateContent(child)
		}
	}
	return result
}

func standardBYOTemplateDigest(template map[string]any) (string, error) {
	raw, err := synthesis.ARMTemplate()
	if err != nil {
		return "", err
	}
	var standard map[string]any
	if err := json.Unmarshal(raw, &standard); err != nil {
		return "", err
	}
	actual, err := byoTemplateDigest(template)
	if err != nil {
		return "", err
	}
	expected, err := byoTemplateDigest(standard)
	if err != nil {
		return "", err
	}
	if actual != expected {
		return "", fmt.Errorf("BYO AKS requires the standard deployment template; regenerate the Bicep files")
	}
	return actual, nil
}

func byoBindingDigest(parameters map[string]any) (string, error) {
	immutable := make(map[string]any, len(parameters))
	for key, value := range parameters {
		if key != "deployments" && key != "tags" {
			immutable[key] = value
		}
	}
	return onboarding.Digest(immutable)
}
