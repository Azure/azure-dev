// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// connectionAddFlags carries the verb-specific flags for `connection add`.
type connectionAddFlags struct {
	index        string
	instanceName string
	fromFile     string
}

// newToolboxConnectionAddCommand returns the `connection add` command.
func newToolboxConnectionAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &connectionAddFlags{}

	cmd := &cobra.Command{
		Use:   "add <toolbox> [connection]",
		Short: "Attach one or more connections to a toolbox.",
		Long: `Attach one or more tools to a toolbox and publish a new default version.

This command has two modes:

Single-connection mode:

  azd ai toolbox connection add <toolbox> <connection> [--index <name>] [--instance-name <name>]

Pass the project connection's short name as the positional. --index is
required when the connection's category is CognitiveSearch (Azure AI Search).
--instance-name is required when the category is GroundingWithCustomSearch.
Only one tool is appended; the new version becomes the default.

File mode:

  azd ai toolbox connection add <toolbox> --from-file <path>

Provide a JSON or YAML file with multiple connections. All inputs from a
single invocation publish exactly one new toolbox version, so adding three
connections this way produces v(N+1), not v(N+3).

` + fileShapeBlurb(false) + `

At least one connection must be provided.

Examples:

  # Attach a single RemoteTool (MCP) connection
  azd ai toolbox connection add research my-mcp

  # Attach a CognitiveSearch connection with an explicit index
  azd ai toolbox connection add research my-search --index products

  # Attach a GroundingWithCustomSearch connection with a Bing custom-search instance
  azd ai toolbox connection add research my-bing --instance-name docs-config

  # Attach several tools in one new version
  azd ai toolbox connection add research --from-file ./tools.yaml --output json
`,
		Args: func(cmd *cobra.Command, args []string) error {
			fromFile, _ := cmd.Flags().GetString("from-file")
			if strings.TrimSpace(fromFile) != "" {
				if len(args) != 1 {
					return cobra.ExactArgs(1)(cmd, args)
				}
				return nil
			}
			if len(args) != 2 {
				return cobra.RangeArgs(2, 2)(cmd, args)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			connName := ""
			if len(args) > 1 {
				connName = args[1]
			}
			return runConnectionAdd(
				cmd.Context(), args[0], connName, *flags,
				readToolboxFlags(cmd, extCtx),
				defaultConnectionResolver{},
			)
		},
	}

	cmd.Flags().StringVar(
		&flags.index, "index", "",
		"Search index name. Only valid for CognitiveSearch (Azure AI Search) connections; required there.",
	)
	cmd.Flags().StringVar(
		&flags.instanceName, "instance-name", "",
		"Bing custom-search configuration name. "+
			"Only valid for GroundingWithCustomSearch connections; required there.",
	)
	cmd.Flags().StringVar(
		&flags.fromFile, "from-file", "",
		"Path to a JSON/YAML file describing the connections to add (see --help for the file shape).",
	)
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runConnectionAdd(
	ctx context.Context, toolboxName, connName string,
	verb connectionAddFlags, parent toolboxFlags,
	resolver connectionResolver,
) error {
	if err := validateToolboxName(toolboxName); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	if strings.TrimSpace(verb.fromFile) == "" && strings.TrimSpace(connName) == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidPositionalArg,
			"<connection> must not be empty when --from-file is not set",
			"pass a connection name or use --from-file",
		)
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox connection add", resolved)

	return runConnectionAddWith(ctx, client, resolver, resolved.Endpoint, toolboxName, connName, verb, parent)
}

// runConnectionAddWith is the testable core.
func runConnectionAddWith(
	ctx context.Context,
	client toolboxClient,
	resolver connectionResolver,
	endpoint, toolboxName, connName string,
	verb connectionAddFlags,
	parent toolboxFlags,
) error {
	tb, err := client.GetToolbox(ctx, toolboxName)
	if err != nil {
		if isAzureNotFound(err) {
			return exterrors.Dependency(
				exterrors.CodeToolboxNotFound,
				fmt.Sprintf("toolbox %q not found", toolboxName),
				fmt.Sprintf("run 'azd ai toolbox create %q --from-file <file>' first", toolboxName),
			)
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox)
	}

	current, err := client.GetToolboxVersion(ctx, toolboxName, tb.DefaultVersion)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolboxVersion)
	}

	newEntries := []map[string]any{}
	addedConnectionNames := []string{}

	if strings.TrimSpace(verb.fromFile) != "" {
		if strings.TrimSpace(connName) != "" {
			return exterrors.Validation(
				exterrors.CodeInvalidPositionalArg,
				"do not pass <connection> when --from-file is set",
				"either pass a single connection positional or use --from-file",
			)
		}
		if verb.index != "" {
			return exterrors.Validation(
				exterrors.CodeUnsupportedIndexFlag,
				"--index cannot be used together with --from-file",
				"set connection indexes in the file under connections[].index",
			)
		}
		if verb.instanceName != "" {
			return exterrors.Validation(
				exterrors.CodeUnsupportedInstanceNameFlag,
				"--instance-name cannot be used together with --from-file",
				"set connection instance names in the file under connections[].instance_name",
			)
		}

		var input toolboxToolsFile
		if err := parseToolboxFile(verb.fromFile, &input); err != nil {
			return err
		}
		resolvedEntries, err := resolveConnectionSpecs(ctx, resolver, endpoint, input.Connections)
		if err != nil {
			return err
		}
		for _, c := range input.Connections {
			addedConnectionNames = append(addedConnectionNames, c.Name)
		}
		newEntries = append(newEntries, resolvedEntries...)
	} else {
		conn, err := resolver.resolveConnection(ctx, endpoint, connName)
		if err != nil {
			return err
		}
		entry, err := buildToolEntry(conn, verb.index, verb.instanceName)
		if err != nil {
			return err
		}
		newEntries = append(newEntries, entry)
		addedConnectionNames = append(addedConnectionNames, conn.Name)
	}

	if len(newEntries) == 0 {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			"no connections to add",
			"provide at least one connection in 'connections[]'",
		)
	}

	if err := rejectDuplicatesAgainstCurrentAndBatch(current.Tools, newEntries); err != nil {
		return err
	}

	newTools := slices.Clone(current.Tools)
	newTools = append(newTools, newEntries...)

	req := &azure.CreateToolboxVersionRequest{
		Description: current.Description,
		Metadata:    current.Metadata,
		Tools:       newTools,
	}
	created, err := client.CreateToolboxVersion(ctx, toolboxName, req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateToolboxVersion)
	}

	if _, err := client.SetDefaultVersion(ctx, toolboxName, created.Version); err != nil {
		return exterrors.Dependency(
			exterrors.CodeSetDefaultVersionFailed,
			fmt.Sprintf(
				"toolbox %q version %q was created but could not be promoted to default: %s",
				toolboxName, created.Version, err,
			),
			fmt.Sprintf(
				"run `azd ai toolbox update %q --default-version %q` to retarget the default",
				toolboxName, created.Version,
			),
		)
	}

	return emitConnectionAddResult(toolboxName, created.Version, addedConnectionNames, parent.output, endpoint)
}

// rejectDuplicatesAgainstCurrentAndBatch flags a duplicate if any new entry
// references a connection that's already in the current tools or appears more
// than once within the new batch itself.
func rejectDuplicatesAgainstCurrentAndBatch(current, added []map[string]any) error {
	seenCurrent := map[string]struct{}{}
	forEachToolConnectionID(current, func(id string) bool {
		seenCurrent[id] = struct{}{}
		return false
	})

	seenNew := map[string]struct{}{}
	dupInNew := ""
	forEachToolConnectionID(added, func(id string) bool {
		if _, ok := seenNew[id]; ok {
			dupInNew = id
			return true
		}
		seenNew[id] = struct{}{}
		if _, exists := seenCurrent[id]; exists {
			dupInNew = id
			return true
		}
		return false
	})

	if dupInNew != "" {
		return exterrors.Validation(
			exterrors.CodeDuplicateConnection,
			fmt.Sprintf("connection %q is already attached or duplicated in input", dupInNew),
			"remove duplicate connection entries",
		)
	}
	return nil
}

// emitConnectionAddResult prints the standard output for a successful add.
func emitConnectionAddResult(
	toolboxName, newVersion string, connectionNames []string, output, endpoint string,
) error {
	mcpURL := buildToolboxMcpURL(endpoint, toolboxName, newVersion)
	if output == "json" {
		payload := map[string]any{
			"toolbox":     toolboxName,
			"version":     newVersion,
			"connections": connectionNames,
			"endpoint":    mcpURL,
		}
		return emitJSON(payload)
	}

	fmt.Printf("Attached connection(s) to toolbox %s (now at version %s).\n", toolboxName, newVersion)
	if len(connectionNames) > 0 {
		fmt.Printf("Connections: %s\n", strings.Join(connectionNames, ", "))
	}
	fmt.Printf("Endpoint: %s\n", mcpURL)
	return nil
}
