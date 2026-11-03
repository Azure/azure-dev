// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"azure.ai.content-understanding/internal/armclient"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type serviceSession struct {
	client            cuclient.Client
	apiVersion        string
	analyzer          string
	endpoint          string
	environment       string
	models            map[string]string
	scope             config.ResourceScope
	sources           map[string]string
	resolveAccount    func(context.Context, string, string, string) (armclient.Account, error)
	clientForEndpoint func(string) (cuclient.Client, error)
	confirm           func(context.Context, string) (bool, error)
	close             func()
}

type sessionFactory func(*cobra.Command, *azdext.ExtensionContext, config.Options) (*serviceSession, error)

func openServiceSession(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, options config.Options,
) (*serviceSession, error) {
	runtime, err := newRuntimeContext(cmd, extCtx, options)
	if err != nil {
		return nil, err
	}
	client, err := cuclient.NewRESTTransport(runtime.Config.Endpoint, runtime.Credential, &cuclient.TransportOptions{
		APIVersion: runtime.Config.APIVersion, Version: Version,
	})
	if err != nil {
		runtime.Close()
		return nil, exterrors.FromError(err)
	}
	return &serviceSession{
		client: client, apiVersion: runtime.Config.APIVersion,
		analyzer: runtime.Config.DefaultAnalyzer, close: runtime.Close,
		endpoint: runtime.Config.Endpoint, environment: runtime.Config.Environment,
		models:  runtime.Config.ModelDeployments,
		scope:   runtime.Config.Scope,
		sources: runtime.Config.Sources,
		resolveAccount: func(ctx context.Context, resource, subscription, group string) (armclient.Account, error) {
			resolver, err := armclient.NewResolver(runtime.Credential, nil)
			if err != nil {
				return armclient.Account{}, err
			}
			return resolver.Resolve(ctx, resource, subscription, group, runtime.Config.Scope)
		},
		clientForEndpoint: func(endpoint string) (cuclient.Client, error) {
			return cuclient.NewRESTTransport(endpoint, runtime.Credential, &cuclient.TransportOptions{
				APIVersion: runtime.Config.APIVersion, Version: Version,
			})
		},
		confirm: func(ctx context.Context, message string) (bool, error) {
			response, err := runtime.Client.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
				Options: &azdext.ConfirmOptions{Message: message, DefaultValue: new(false)},
			})
			if err != nil {
				return false, err
			}
			if response == nil || response.Value == nil {
				return false, &azdext.LocalError{
					Code: "invalid_confirmation_response", Category: azdext.LocalErrorCategoryDependency,
					Message: "The azd host did not return a confirmation decision.",
				}
			}
			return response.GetValue(), nil
		},
	}, nil
}

func serviceCommand(cmd *cobra.Command, options *config.Options) *cobra.Command {
	cmd.Flags().StringVar(&options.Endpoint, "endpoint", "",
		"Azure CU resource endpoint (services.ai.azure.com or cognitiveservices.azure.com; HTTPS port 443)")
	cmd.Flags().StringVar(&options.APIVersion, "api-version", "",
		"CU API version: 2025-11-01 or YYYY-MM-DD-preview (default: resolved setting or GA)")
	return jsonCommand(cmd)
}

func jsonCommand(cmd *cobra.Command) *cobra.Command {
	return azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"default", "json", "none"},
		Usage: "Output format: default (complete JSON), json, or none",
	})
}

func commandError(code, message, suggestion string) error {
	return &cuclient.Error{
		Kind: cuclient.ErrorValidation, Code: code, Message: message, Suggestion: suggestion,
	}
}

func writeServiceResult(cmd *cobra.Command, format string, result any) error {
	if err := cmd.Context().Err(); err != nil {
		return exterrors.FromError(err)
	}
	if format == "none" {
		return nil
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("write CU result: %w", err)
	}
	return nil
}

func analyzerJSON(analyzer cuclient.Analyzer) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(analyzer.JSON, &object); err != nil || object == nil {
		return nil, &cuclient.Error{
			Kind: cuclient.ErrorService, Code: "invalid_analyzer_response",
			Message: "The CU client returned an invalid analyzer definition.",
		}
	}
	return analyzer.JSON, nil
}
