// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

var (
	// ManagedHarness controls prompt agents using a Copilot harness.
	ManagedHarness = Feature{
		ID:     "agents.managed_harness",
		EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_MANAGED_HARNESS",
	}
	// Voice controls managed voice and hosted voice agents.
	Voice = Feature{
		ID:     "agents.voice",
		EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE",
	}
	// LongRunning controls starting background Responses invocations.
	LongRunning = Feature{
		ID:     "agents.long_running",
		EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_LONG_RUNNING",
	}
)

type environmentKey struct{}
type servicesKey struct{}

// WithServices includes service env before manifest adoption.
func WithServices(ctx context.Context, services map[string]*azdext.ServiceConfig) context.Context {
	return context.WithValue(ctx, servicesKey{}, services)
}

// WithEnvironment preserves the SDK environment for nested calls.
func WithEnvironment(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, environmentKey{}, name)
}

// Environment returns the SDK or host lifecycle selection.
func Environment(ctx context.Context) string {
	if name, ok := ctx.Value(environmentKey{}).(string); ok {
		return name
	}
	return os.Getenv("AZD_ENVIRONMENT")
}

// Require rejects a disabled preview before its operation starts.
func Require(ctx context.Context, client *azdext.AzdClient, feature Feature) error {
	enabled, err := IsEnabled(ctx, client, feature, Environment(ctx))
	if err != nil {
		return err
	}
	if services, ok := ctx.Value(servicesKey{}).(map[string]*azdext.ServiceConfig); ok {
		for _, service := range services {
			enabled = envEnabled(service.GetEnvironment()[feature.EnvVar]) || enabled
		}
	}
	if enabled {
		return nil
	}
	return exterrors.Compatibility(
		exterrors.CodeFeatureNotEnabled,
		fmt.Sprintf("preview feature %q is disabled", feature.ID),
		fmt.Sprintf(
			"enable with 'azd config set alpha.%s on' or 'azd env set %s true' "+
				"in the selected environment; alternatively set %s=true in the process "+
				"or azure.yaml services[].env",
			feature.ID, feature.EnvVar, feature.EnvVar,
		),
	)
}

// RequiredForDefinition classifies a resolved agent definition.
// It accepts authoring and data-plane shapes without mutating either.
func RequiredForDefinition(definition map[string]any) []Feature {
	kind, _ := definition["kind"].(string)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "prompt":
		harness, _ := definition["harness"].(map[string]any)
		harnessType, _ := harness["type"].(string)
		if agent_api.IsCopilotHarness(harnessType) {
			return []Feature{ManagedHarness}
		}
	case "voice", "prompt-voice":
		return []Feature{Voice}
	case "hosted":
		protocols, _ := definition["protocols"].([]any)
		for _, entry := range protocols {
			protocol, _ := entry.(map[string]any)
			if protocol["protocol"] == "invocations_ws" {
				return []Feature{Voice}
			}
		}
	}
	return nil
}

// RequireDefinition enforces only the previews a definition uses.
func RequireDefinition(ctx context.Context, client *azdext.AzdClient, definition map[string]any) error {
	if env, ok := definition["env"].(map[string]any); ok {
		values := make(map[string]string, len(env))
		for key, value := range env {
			values[key] = fmt.Sprint(value)
		}
		services, _ := ctx.Value(servicesKey{}).(map[string]*azdext.ServiceConfig)
		copyServices := make(map[string]*azdext.ServiceConfig, len(services)+1)
		maps.Copy(copyServices, services)
		copyServices[""] = &azdext.ServiceConfig{Environment: values}
		ctx = WithServices(ctx, copyServices)
	}
	for _, feature := range RequiredForDefinition(definition) {
		if err := Require(ctx, client, feature); err != nil {
			return err
		}
	}
	return nil
}
