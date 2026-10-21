// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type serviceSession struct {
	client     cuclient.Client
	apiVersion string
	confirm    func(context.Context, string) (bool, error)
	close      func()
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
		client: client, apiVersion: runtime.Config.APIVersion, close: runtime.Close,
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
