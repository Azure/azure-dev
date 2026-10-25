// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// TemplateDigest binds the actual template content independently of Bicep and ARM service hash algorithms.
func TemplateDigest(template any) (string, error) {
	object, ok := template.(map[string]any)
	if !ok || len(object) == 0 {
		return "", fmt.Errorf("deployment template content is unavailable")
	}
	raw, err := json.Marshal(templateBindingContent(object))
	if err != nil {
		return "", fmt.Errorf("encode deployment template binding: %w", err)
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
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
			// ARM export capitalizes declaration types. Do not case-fold resource types or authored values.
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

func verifyAttemptTemplate(
	ctx context.Context, client *armresources.DeploymentsClient, name string, attempt FoundryAttempt,
) error {
	exported, err := client.ExportTemplateAtSubscriptionScope(ctx, name, nil)
	if err != nil {
		return fmt.Errorf("read exact recorded deployment template binding: %w", err)
	}
	actual, err := TemplateDigest(exported.Template)
	if err != nil {
		return err
	}
	expected := attempt.TemplateDigest
	if expected == "" {
		// Old journals stored the Bicep generator hash, not ARM's independent service hash.
		// Only this trusted standard version can recover an old journal without a content digest.
		raw, err := synthesis.ARMTemplate()
		if err != nil {
			return err
		}
		var standard struct {
			Metadata struct {
				Generator struct {
					TemplateHash string `json:"templateHash"`
				} `json:"_generator"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(raw, &standard); err != nil {
			return err
		}
		if attempt.TemplateHash == "" || attempt.TemplateHash != standard.Metadata.Generator.TemplateHash {
			return fmt.Errorf("recorded deployment's legacy template binding has no trusted standard version")
		}
		var template map[string]any
		if err := json.Unmarshal(raw, &template); err != nil {
			return err
		}
		expected, err = TemplateDigest(template)
		if err != nil {
			return err
		}
	}
	if actual != expected {
		return fmt.Errorf("recorded deployment's actual template binding is unverified or changed")
	}
	return nil
}
