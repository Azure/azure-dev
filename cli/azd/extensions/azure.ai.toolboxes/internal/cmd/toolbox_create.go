// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/projectctx"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// toolboxCreateFlags holds the verb-specific flags for `toolbox create`.
type toolboxCreateFlags struct {
	fromFile string
}

// newToolboxCreateCommand returns the `azd ai toolbox create <name>` command.
func newToolboxCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &toolboxCreateFlags{}

	cmd := &cobra.Command{
		Use:   "create <name> --from-file <path>",
		Short: "Create a toolbox and publish its initial version from a file.",
		Long: `Create a toolbox and publish its initial version.

The Foundry service requires the initial version to ship with a non-empty
connection list, so 'create' takes its inputs from a JSON or YAML file via
--from-file.

` + fileShapeBlurb(true) + `

At least one connection must be provided.

Examples:

  azd ai toolbox create research --from-file ./tools.json
  azd ai toolbox create research --from-file ./tools.yaml --output json
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxCreate(
				cmd.Context(), args[0], *flags, readToolboxFlags(cmd, extCtx),
				defaultConnectionResolver{},
			)
		},
	}

	cmd.Flags().StringVar(
		&flags.fromFile, "from-file", "",
		"Path to a JSON/YAML file describing the initial version (see --help for the file shape).",
	)
	if err := cmd.MarkFlagRequired("from-file"); err != nil {
		panic(err) // never fails; flag is registered above
	}
	registerToolboxOutputFlag(cmd)

	return cmd
}

func runToolboxCreate(
	ctx context.Context, name string, verb toolboxCreateFlags,
	parent toolboxFlags, resolver connectionResolver,
) error {
	if err := validateToolboxName(name); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{FlagValue: parent.projectEndpoint})
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox create", resolved)

	client, err := newToolboxClient(resolved.Endpoint)
	if err != nil {
		return err
	}

	return runToolboxCreateWith(ctx, client, resolver, resolved.Endpoint, name, verb, parent)
}

// runToolboxCreateWith is the testable core. It accepts injected client and
// resolver so unit tests can drive every branch without an HTTP server.
func runToolboxCreateWith(
	ctx context.Context,
	client toolboxClient,
	resolver connectionResolver,
	endpoint, name string,
	verb toolboxCreateFlags,
	parent toolboxFlags,
) error {
	if _, err := client.GetToolbox(ctx, name); err == nil {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			fmt.Sprintf("toolbox %q already exists", name),
			"run 'azd ai toolbox update' or 'connection add/remove' to change it",
		)
	} else if !isAzureNotFound(err) {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox)
	}

	description := ""
	entries := []map[string]any{}
	var policies map[string]any

	if strings.TrimSpace(verb.fromFile) != "" {
		var input toolboxCreateFile
		if err := parseToolboxFile(verb.fromFile, &input); err != nil {
			return err
		}
		description = input.Description
		policies = input.Policies
		resolvedEntries, err := resolveConnectionSpecs(ctx, resolver, endpoint, input.Connections)
		if err != nil {
			return err
		}
		entries = append(entries, resolvedEntries...)
		entries = append(entries, input.Tools...)
	}

	if len(entries) == 0 {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			"toolbox create requires at least one connection or tool",
			"pass --from-file with a non-empty 'connections' or 'tools' list",
		)
	}

	if err := validateNoDuplicateConnectionIDs(entries); err != nil {
		return err
	}

	req := &azure.CreateToolboxVersionRequest{
		Description: description,
		Tools:       entries,
		Policies:    policies,
	}
	created, err := client.CreateToolboxVersion(ctx, name, req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateToolboxVersion)
	}

	return emitCreateResult(name, created.Version, parent.output, endpoint)
}

func emitCreateResult(name, version, output, endpoint string) error {
	mcpURL := buildToolboxMcpURL(endpoint, name, version)
	if output == "json" {
		return emitJSON(map[string]any{
			"toolbox":  name,
			"version":  version,
			"endpoint": mcpURL,
		})
	}

	fmt.Printf("Created toolbox %s at version %s.\n", name, version)
	fmt.Printf("Endpoint: %s\n", mcpURL)
	return nil
}

// resolveConnectionSpecs walks the file's connection list and converts each
// entry into a service tool entry via the connection resolver and buildToolEntry.
func resolveConnectionSpecs(
	ctx context.Context,
	resolver connectionResolver,
	endpoint string,
	specs []toolboxConnectionSpec,
) ([]map[string]any, error) {
	entries := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		if strings.TrimSpace(spec.Name) == "" {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"connection name must not be empty",
				"set connections[].name in the input file",
			)
		}
		conn, err := resolver.resolveConnection(ctx, endpoint, spec.Name)
		if err != nil {
			return nil, err
		}
		entry, err := buildToolEntry(conn, spec.Index, spec.InstanceName)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// validateNoDuplicateConnectionIDs rejects entries that reference the same
// project_connection_id more than once.
func validateNoDuplicateConnectionIDs(entries []map[string]any) error {
	ids := []string{}
	forEachToolConnectionID(entries, func(id string) bool {
		ids = append(ids, id)
		return false
	})

	slices.Sort(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return exterrors.Validation(
				exterrors.CodeDuplicateConnection,
				fmt.Sprintf("connection %q appears more than once in the input", ids[i]),
				"remove duplicate connection entries",
			)
		}
	}
	return nil
}
