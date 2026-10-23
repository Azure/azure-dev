// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"context"
	"errors"

	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Load reads one environment snapshot before resolving configuration and the credential's tenant.
// A runtime endpoint without an environment selection deliberately avoids project/deployment RPCs.
func Load(
	ctx context.Context, client *azdext.AzdClient, environment string, options Options, process map[string]string,
) (Resolved, error) {
	if err := ctx.Err(); err != nil {
		return Resolved{}, err
	}
	if err := validateRuntime(process); err != nil {
		return Resolved{}, err
	}
	environment = first(environment, process["AZD_ENVIRONMENT"])
	var values map[string]string
	var section []byte
	useDefault := environment == "" && first(options.Endpoint, process["CU_ENDPOINT"]) == ""
	if environment != "" || useDefault {
		var selected *azdext.Environment
		if useDefault {
			response, err := client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
			if err != nil && !noCurrentEnvironment(err) {
				return Resolved{}, environmentError(err)
			}
			if err != nil {
				useDefault = false
			}
			selected = response.GetEnvironment()
		} else {
			response, err := client.Environment().Get(ctx, &azdext.GetEnvironmentRequest{Name: environment})
			if err != nil {
				return Resolved{}, environmentError(err)
			}
			selected = response.GetEnvironment()
		}
		if selected != nil || useDefault || environment != "" {
			if selected.GetName() == "" || (environment != "" && selected.GetName() != environment) {
				return Resolved{}, invalid("invalid_environment", "The azd host did not return the requested environment.")
			}
			environment = selected.GetName()
			response, err := client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: environment})
			if err != nil {
				return Resolved{}, environmentError(err)
			}
			if response == nil {
				return Resolved{}, invalid("invalid_environment", "The azd host did not return environment values.")
			}
			values = make(map[string]string)
			for _, entry := range response.GetKeyValues() {
				if entry.GetKey() == "" {
					return Resolved{}, invalid("invalid_environment", "The azd host returned an invalid environment value.")
				}
				if _, duplicate := values[entry.GetKey()]; duplicate {
					return Resolved{}, invalid("invalid_environment", "The azd host returned duplicate environment values.")
				}
				values[entry.GetKey()] = entry.GetValue()
			}
			config, err := client.Environment().GetConfigSection(ctx, &azdext.GetConfigSectionRequest{
				Path: "cu", EnvName: environment,
			})
			if err != nil {
				return Resolved{}, environmentError(err)
			}
			if config == nil || (config.GetFound() && len(config.GetSection()) == 0) {
				return Resolved{}, invalid("invalid_environment_config",
					"The azd host returned an invalid cu configuration section.")
			}
			if config.GetFound() {
				section = config.GetSection()
			}
		}
	}
	user, err := ReadUserDefaults(ctx, client)
	if err != nil {
		return Resolved{}, err
	}
	result, err := resolve(options, process, values, section, user)
	if err != nil {
		return Resolved{}, err
	}
	result.Environment = environment
	for name, source := range result.Sources {
		if source == "azd environment" {
			result.Sources[name] = source + ": " + environment
		}
	}
	if result.TenantID == "" && useDefault {
		// Deployment RPCs always use azd's default environment, never a named selection.
		deployment, err := client.Deployment().GetDeploymentContext(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return Resolved{}, environmentError(err)
		}
		scope := deployment.GetAzureContext().GetScope()
		result.TenantID = scope.GetTenantId()
		if result.Scope.ResourceID == "" {
			result.Scope.SubscriptionID = first(result.Scope.SubscriptionID, scope.GetSubscriptionId())
			result.Scope.ResourceGroup = first(result.Scope.ResourceGroup, scope.GetResourceGroup())
		}
	}
	if result.TenantID == "" && !useDefault && result.Scope.SubscriptionID != "" {
		tenant, err := client.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
			SubscriptionId: result.Scope.SubscriptionID,
		})
		if err != nil {
			return Resolved{}, environmentError(err)
		}
		result.TenantID = tenant.GetTenantId()
		if result.TenantID == "" {
			return Resolved{}, exterrors.MissingTenant()
		}
	}
	if result.TenantID == "" && environment != "" {
		return Resolved{}, exterrors.MissingTenant()
	}
	return result, nil
}

func noCurrentEnvironment(err error) bool {
	// The v1 host returns these sentinels as untyped gRPC Unknown errors.
	// Match only the exact known absence cases; never swallow other discovery failures.
	if status.Code(err) != codes.Unknown {
		return false
	}
	switch status.Convert(err).Message() {
	case "no project exists; to create a new project, run `azd init`", "default environment not found":
		return true
	default:
		return false
	}
}

func environmentError(err error) error {
	safe := exterrors.FromError(err)
	if local, ok := errors.AsType[*azdext.LocalError](safe); ok && local.Code == exterrors.CodeUnexpected {
		return &azdext.LocalError{
			Code: exterrors.CodeEnvironmentUnavailable, Category: azdext.LocalErrorCategoryDependency,
			Message: "The azd environment, subscription, or deployment context could not be loaded.",
			Suggestion: "Select a valid environment, or supply a runtime endpoint " +
				"without --environment or AZD_ENVIRONMENT.",
		}
	}
	return safe
}
