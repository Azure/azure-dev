// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	"azure.ai.content-understanding/internal/defaults"
)

// PostRequest is resolved from one explicitly selected azd environment snapshot.
type PostRequest struct {
	Endpoint, ResourceGroup, Account, Subscription, APIVersion, Selection string
	SetupComplete, ForceConfiguration, DisableConfiguration               bool
}

type Deployment struct {
	Name, Model, Version, Format, State string
}

// PostCapabilities contains no key-auth capability: the native extension is Entra-only.
type PostCapabilities interface {
	SetupModels(context.Context) error
	MarkComplete(context.Context) error
	HasConfiguration(context.Context) (bool, error)
	SetConfiguration(context.Context, string, string) error
	ListDeployments(context.Context) ([]Deployment, error)
	ConfigureDefaults(context.Context, map[string]string) error
}

type PostResult struct {
	Succeeded                  bool     `json:"succeeded"`
	EnvironmentConfigured      bool     `json:"environmentConfigured"`
	EnvironmentPreserved       bool     `json:"environmentPreserved"`
	ConfigurationSetupDisabled bool     `json:"configurationSetupDisabled"`
	ModelSetup                 string   `json:"modelSetup"`
	DefaultsConfigured         bool     `json:"defaultsConfigured"`
	GenerativeReady            bool     `json:"generativeReady"`
	Warnings                   []string `json:"warnings"`
	Messages                   []string `json:"messages"`
}

// Postprovision preserves optional failures explicitly in warnings, never swallowing cancellation.
func Postprovision(ctx context.Context, request PostRequest, capabilities PostCapabilities) (PostResult, error) {
	result := PostResult{Succeeded: true, ModelSetup: "pending", Warnings: []string{}, Messages: []string{}}
	warn := func(message string) {
		result.Warnings = append(result.Warnings, message)
		result.Messages = append(result.Messages, message)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if request.SetupComplete {
		result.ModelSetup = "skipped"
		result.Messages = append(result.Messages, "Model setup already completed; skipping it.")
	} else {
		err := capabilities.SetupModels(ctx)
		if err == nil {
			err = capabilities.MarkComplete(ctx)
		}
		if err := interrupted(ctx, err); err != nil {
			return result, err
		}
		if err != nil {
			result.ModelSetup = "failed"
			warn("Optional model setup failed. Check azd sign-in, CU access, model quota, " +
				"and CU_MODEL_SELECTION; rerun azd up.")
		} else {
			result.ModelSetup = "completed"
			if strings.EqualFold(request.Selection, "none") {
				result.Messages = append(result.Messages, "No model deployments selected.")
			}
		}
	}
	if request.DisableConfiguration {
		result.ConfigurationSetupDisabled = true
		result.Messages = append(result.Messages, "Automatic CU configuration setup disabled.")
		return result, ctx.Err()
	}
	populated, err := capabilities.HasConfiguration(ctx)
	if err := interrupted(ctx, err); err != nil {
		return result, err
	}
	if err != nil {
		result.Succeeded = false
		warn("Could not inspect existing CU configuration; refusing to overwrite it.")
		return result, ctx.Err()
	}
	if populated && !request.ForceConfiguration {
		result.EnvironmentPreserved = true
		result.Messages = append(result.Messages, "Existing CU configuration has saved values; preserving it.")
		return result, ctx.Err()
	}
	failures := []string{}
	for _, write := range [][3]string{
		{"endpoint", request.Endpoint, "Could not configure the cu CLI endpoint"},
		{"default_analyzer", "prebuilt-layout", "Could not configure the default analyzer"},
	} {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := capabilities.SetConfiguration(ctx, write[0], write[1]); err != nil {
			if err := interrupted(ctx, err); err != nil {
				return result, err
			}
			failures = append(failures, write[2])
		}
	}
	deployments, err := capabilities.ListDeployments(ctx)
	if err := interrupted(ctx, err); err != nil {
		return result, err
	}
	if err != nil {
		warn("Could not inspect optional model deployments.")
		deployments = nil
	}
	mappings := map[string]string{}
	completion, embedding := false, false
	for _, deployment := range deployments {
		mappings[deployment.Model] = deployment.Name
		if strings.EqualFold(deployment.State, "Succeeded") {
			if strings.HasPrefix(deployment.Model, "text-embedding-") {
				embedding = true
			} else {
				completion = true
			}
		}
	}
	enriched := defaults.WithAliases(mappings)
	// Persist the entire map as JSON at the adapter boundary; model names contain dots.
	if len(enriched) > 0 {
		for _, model := range slices.Sorted(maps.Keys(enriched)) {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := capabilities.SetConfiguration(ctx, "model_deployments."+model, enriched[model]); err != nil {
				if err := interrupted(ctx, err); err != nil {
					return result, err
				}
				warn("Could not save an optional model mapping.")
			}
		}
		if err := capabilities.ConfigureDefaults(ctx, enriched); err != nil {
			if err := interrupted(ctx, err); err != nil {
				return result, err
			}
			warn("Could not initialize Foundry defaults automatically.")
		} else {
			result.DefaultsConfigured = true
			result.Messages = append(result.Messages, "Foundry defaults initialized.")
		}
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Messages = append(result.Messages,
		"Available without an LLM or embeddings model: prebuilt-digitalParse, prebuilt-read, prebuilt-layout")
	result.GenerativeReady = result.DefaultsConfigured && completion && embedding
	switch {
	case result.GenerativeReady:
		result.Messages = append(result.Messages,
			"Model readiness verified: LLM and embeddings model deployments succeeded "+
				"and Content Understanding defaults are configured.")
	case result.ModelSetup == "failed":
		result.Messages = append(result.Messages,
			"The Microsoft Foundry resource and CU configuration remain usable; resolve the "+
				"optional model issue and rerun azd up.")
	case len(mappings) > 0 && !result.DefaultsConfigured:
		result.Messages = append(result.Messages,
			"Generative AI workflows are not ready because defaults configuration failed.")
	case len(mappings) > 0:
		result.Messages = append(result.Messages,
			"Generative AI workflows require succeeded LLM and embeddings deployments.")
	default:
		result.Messages = append(result.Messages,
			"No optional models were configured; content extraction analyzers remain available.")
	}
	result.Messages = append(result.Messages, failures...)
	if len(failures) > 0 {
		result.Succeeded = false
		result.Messages = append(result.Messages, "Azure provisioning completed, but cu auto-configuration is incomplete.",
			"Run the frontend's doctor command for actionable diagnostics.")
	}
	result.EnvironmentConfigured = result.Succeeded
	return result, nil
}

func interrupted(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
