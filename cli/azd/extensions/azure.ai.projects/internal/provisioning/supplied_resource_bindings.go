// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/google/uuid"
)

func (p *FoundryProvisioningProvider) readBYOIdentityPrincipal(ctx context.Context, resourceID string) (string, error) {
	id, err := parseBYOResourceID(resourceID, "Microsoft.ManagedIdentity/userAssignedIdentities")
	if err != nil {
		return "", err
	}
	client, err := armmsi.NewUserAssignedIdentitiesClient(id.SubscriptionID, p.credential, byoARMOptions(p.armClientOptions))
	if err != nil {
		return "", fmt.Errorf("create supplied identity client: %w", err)
	}
	identity, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return "", fmt.Errorf("read supplied identity deployment binding: %w", err)
	}
	if identity.ID == nil || !strings.EqualFold(*identity.ID, resourceID) ||
		identity.Properties == nil || identity.Properties.PrincipalID == nil {
		return "", fmt.Errorf("supplied identity deployment binding requires a matching resource ID and principal ID")
	}
	principal := *identity.Properties.PrincipalID
	if _, err := uuid.Parse(principal); err != nil {
		return "", fmt.Errorf("supplied identity principal ID must be a GUID: %w", err)
	}
	return principal, nil
}

func validateBYOSupportingTemplate(template map[string]any) error {
	raw, err := synthesis.ARMTemplate()
	if err != nil {
		return fmt.Errorf("load supporting-resource deployment template: %w", err)
	}
	var standard map[string]any
	if err := json.Unmarshal(raw, &standard); err != nil {
		return fmt.Errorf("decode supporting-resource deployment template: %w", err)
	}
	expected, err := json.Marshal(templateBindingContent(standard))
	if err != nil {
		return fmt.Errorf("encode standard deployment graph: %w", err)
	}
	actual, err := json.Marshal(templateBindingContent(template))
	if err != nil {
		return fmt.Errorf("encode supplied-resource deployment graph: %w", err)
	}
	if !bytes.Equal(actual, expected) {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"supplied-resource bindings require the standard BYO AKS deployment graph",
			"re-eject the infrastructure templates; customize supported deployment parameters instead of "+
				"resource naming, creation or authorization expressions")
	}
	return nil
}

func templateBindingContent(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		schema, _ := value["$schema"].(string)
		isTemplate := strings.HasSuffix(strings.ToLower(schema), "deploymenttemplate.json#")
		for key, item := range value {
			if key != "metadata" || !isTemplate {
				if isTemplate && (key == "parameters" || key == "outputs" || key == "definitions") {
					result[key] = templateDeclarationMap(item)
				} else {
					result[key] = templateBindingContent(item)
				}
			}
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = templateBindingContent(item)
		}
		return result
	default:
		return value
	}
}

func templateDeclarationMap(value any) any {
	definitions, ok := value.(map[string]any)
	if !ok {
		return value
	}
	result := make(map[string]any, len(definitions))
	for key, definition := range definitions {
		result[key] = templateDeclaration(definition)
	}
	return result
}

func templateDeclaration(value any) any {
	definition, ok := value.(map[string]any)
	if !ok {
		return value
	}
	result := make(map[string]any, len(definition))
	for key, item := range definition {
		switch key {
		case "type":
			if kind, ok := item.(string); ok {
				switch lower := strings.ToLower(kind); lower {
				case "string", "securestring", "int", "bool", "object", "secureobject", "array":
					item = lower
				}
			}
			result[key] = item
		case "properties":
			result[key] = templateDeclarationMap(item)
		case "items", "additionalProperties":
			result[key] = templateDeclaration(item)
		default:
			result[key] = templateBindingContent(item)
		}
	}
	return result
}
