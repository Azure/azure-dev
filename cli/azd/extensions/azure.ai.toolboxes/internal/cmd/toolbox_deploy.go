// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/projectctx"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newToolboxDeployCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "deploy [path]",
		Short: "Deploy a local toolbox definition.",
		Long: `Deploy a toolbox definition to the configured Foundry project.

The path defaults to ./toolbox.yaml. Each deployment creates a new immutable
toolbox version; it does not provision a Foundry project or its connections.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := definition.DefaultPath
			if len(args) == 1 {
				path = args[0]
			}
			return runToolboxDeploy(cmd.Context(), path, readToolboxFlags(cmd, extCtx), defaultConnectionResolver{})
		},
	}
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runToolboxDeploy(
	ctx context.Context,
	path string,
	parent toolboxFlags,
	resolver connectionResolver,
) error {
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{FlagValue: parent.projectEndpoint})
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox deploy", resolved)
	client, err := newToolboxClient(resolved.Endpoint)
	if err != nil {
		return err
	}
	return runToolboxDeployWith(ctx, client, resolver, resolved.Endpoint, path, parent)
}

func runToolboxDeployWith(
	ctx context.Context,
	client toolboxClient,
	resolver connectionResolver,
	endpoint string,
	path string,
	parent toolboxFlags,
) error {
	input, err := loadLocalToolboxDefinition(path)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			fmt.Sprintf("toolbox definition %q does not declare a name", path),
			"set 'name' in the toolbox definition and retry",
		)
	}
	if err := validateToolboxName(name); err != nil {
		return err
	}

	request, err := buildToolboxVersionRequest(ctx, resolver, endpoint, input)
	if err != nil {
		return err
	}
	created, err := client.CreateToolboxVersion(ctx, name, request)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateToolboxVersion)
	}

	mcpURL := buildToolboxMcpURL(endpoint, name, created.Version)
	if err := setToolboxEndpointEnvFunc(ctx, name, mcpURL, endpoint); err != nil {
		return err
	}
	return emitDeployResult(name, created.Version, path, parent.output, mcpURL)
}

func emitDeployResult(name, version, path, output, mcpURL string) error {
	if output == "json" {
		return emitJSON(map[string]any{
			"toolbox":    name,
			"version":    version,
			"definition": path,
			"endpoint":   mcpURL,
		})
	}
	fmt.Printf("Deployed toolbox %s at version %s from %s.\n", name, version, path)
	fmt.Printf("Endpoint: %s\n", mcpURL)
	return nil
}
