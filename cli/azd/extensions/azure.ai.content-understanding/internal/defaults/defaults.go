// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package defaults

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"

	"azure.ai.content-understanding/internal/cuclient"
)

const (
	Completion     = "prebuilt-analyzer-completion"
	CompletionMini = "prebuilt-analyzer-completion-mini"
	Embedding      = "prebuilt-analyzer-embedding"
)

func ParseModels(values []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, value := range values {
		model, deployment, found := strings.Cut(value, "=")
		model, deployment = strings.TrimSpace(model), strings.TrimSpace(deployment)
		if !found || model == "" || deployment == "" {
			return nil, &cuclient.Error{
				Kind: cuclient.ErrorValidation, Code: "invalid_model_mapping",
				Message:    "Model mappings must contain a nonempty MODEL=DEPLOYMENT pair.",
				Suggestion: "Repeat --model MODEL=DEPLOYMENT for each mapping.",
			}
		}
		result[model] = deployment
	}
	return result, nil
}

func IsNotSet(err error) bool {
	domain, ok := errors.AsType[*cuclient.Error](err)
	if !ok || domain.Code != "DefaultsNotSet" ||
		(domain.StatusCode != 0 && domain.StatusCode != 400 && domain.StatusCode != 404) {
		return false
	}
	return domain.Kind == cuclient.ErrorNotFound || domain.Kind == cuclient.ErrorValidation ||
		domain.Kind == cuclient.ErrorService
}

func Merge(base, overrides map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(overrides))
	maps.Copy(result, base)
	maps.Copy(result, overrides)
	return result
}

// WithAliases follows PR2's model preferences, using sorted fallback names rather
// than depending on Go map iteration or the service's object-property order.
func WithAliases(mapping map[string]string) map[string]string {
	result := Merge(nil, mapping)
	names := slices.Sorted(maps.Keys(mapping))
	var full, mini, embedding string
	for _, name := range names {
		if strings.HasPrefix(name, "prebuilt-analyzer-") {
			continue
		}
		switch {
		case strings.HasPrefix(name, "text-embedding-"):
			if embedding == "" {
				embedding = name
			}
		case strings.HasSuffix(name, "-mini"):
			if mini == "" {
				mini = name
			}
		default:
			if full == "" {
				full = name
			}
		}
	}
	if mapping["gpt-5.2"] != "" {
		full = "gpt-5.2"
	}
	for _, name := range []string{"gpt-5.2-mini", "gpt-4.1-mini", "gpt-4o-mini"} {
		if mapping[name] != "" {
			mini = name
			break
		}
	}
	if mapping["text-embedding-3-large"] != "" {
		embedding = "text-embedding-3-large"
	}
	if full == "" {
		full = mini
	}
	if full != "" {
		result[Completion], result[CompletionMini] = mapping[full], mapping[full]
		if mini != "" {
			result[CompletionMini] = mapping[mini]
		}
	}
	if embedding != "" {
		result[Embedding] = mapping[embedding]
	}
	return result
}

func MissingRequirements(mapping map[string]string) []string {
	embedding, completion := mapping[Embedding] != "", mapping[Completion] != ""
	for name := range mapping {
		if strings.HasPrefix(name, "text-embedding-") {
			embedding = true
		} else if !strings.HasPrefix(name, "prebuilt-analyzer-") {
			completion = true
		}
	}
	missing := []string{}
	if !embedding {
		missing = append(missing, "an embeddings model (for example text-embedding-3-large)")
	}
	if !completion {
		missing = append(missing, "a supported large language model (LLM) deployment")
	}
	if mapping[CompletionMini] == "" {
		missing = append(missing, "Content Understanding's prebuilt analyzer mapping for the selected LLM")
	}
	return missing
}

func Apply(
	ctx context.Context, client cuclient.Client, desired map[string]string, replace bool,
) (cuclient.Defaults, error) {
	if err := ctx.Err(); err != nil {
		return cuclient.Defaults{}, err
	}
	if len(desired) == 0 {
		return cuclient.Defaults{}, MissingModels()
	}
	if err := (cuclient.UpdateDefaultsRequest{ModelDeployments: desired}).Validate(); err != nil {
		return cuclient.Defaults{}, err
	}
	merged := Merge(nil, desired)
	if !replace {
		existing, err := client.GetDefaults(ctx)
		if err != nil && !IsNotSet(err) {
			return cuclient.Defaults{}, err
		}
		if err == nil {
			if _, err := JSON(existing); err != nil {
				return cuclient.Defaults{}, err
			}
			merged = Merge(existing.ModelDeployments, desired)
		}
	}
	if err := ctx.Err(); err != nil {
		return cuclient.Defaults{}, err
	}
	result, err := client.UpdateDefaults(ctx, cuclient.UpdateDefaultsRequest{ModelDeployments: WithAliases(merged)})
	if err != nil {
		return cuclient.Defaults{}, err
	}
	if _, err := JSON(result); err != nil {
		return cuclient.Defaults{}, err
	}
	return result, nil
}

func MissingModels() error {
	return &cuclient.Error{
		Kind: cuclient.ErrorValidation, Code: "missing_model_deployments",
		Message: "No model deployment mappings were supplied.",
		Suggestion: "Pass --model MODEL=DEPLOYMENT or select an azd environment with cu.modelDeployments " +
			"and use --from-environment (or doctor --fix-defaults).",
	}
}

// JSON retains the complete adapter response, or serializes a typed client's
// model mapping when that client does not provide a raw response.
func JSON(result cuclient.Defaults) (json.RawMessage, error) {
	if (cuclient.UpdateDefaultsRequest{ModelDeployments: result.ModelDeployments}).Validate() != nil {
		return nil, invalidResponse()
	}
	if len(result.JSON) != 0 {
		var wire struct {
			ModelDeployments map[string]string `json:"modelDeployments"`
		}
		if err := json.Unmarshal(result.JSON, &wire); err != nil ||
			wire.ModelDeployments == nil || !maps.Equal(wire.ModelDeployments, result.ModelDeployments) {
			return nil, invalidResponse()
		}
		return result.JSON, nil
	}
	return json.Marshal(struct {
		ModelDeployments map[string]string `json:"modelDeployments"`
	}{result.ModelDeployments})
}

func invalidResponse() error {
	return &cuclient.Error{
		Kind: cuclient.ErrorService, Code: "invalid_defaults_response",
		Message: "The CU client returned invalid service defaults.",
	}
}
