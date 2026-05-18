// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// toolboxShowFlags carries the verb-specific flags for `toolbox show`.
type toolboxShowFlags struct {
	version string
}

// newToolboxShowCommand returns the `azd ai agent toolbox show <name>` command.
func newToolboxShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &toolboxShowFlags{}

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a toolbox version, including its computed MCP endpoint.",
		Long: `Show a toolbox.

By default shows the default version. Use --version to inspect a specific
version. The output includes the toolbox's runtime MCP endpoint, which agents
consume via the TOOLBOX_<NAME>_ENDPOINT environment variable convention.

If the toolbox exists only as a pending local record (no version published
yet), the command emits a pending-toolbox view and rejects --version.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxShow(cmd.Context(), args[0], *flags, readToolboxFlags(cmd, extCtx))
		},
	}

	cmd.Flags().StringVar(
		&flags.version, "version", "",
		"Specific version to show. Defaults to the server's default_version.",
	)
	registerToolboxOutputFlag(cmd)

	return cmd
}

func runToolboxShow(
	ctx context.Context, name string, verb toolboxShowFlags, parent toolboxFlags,
) error {
	if err := validateToolboxName(name); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox show", resolved)

	return runToolboxShowWith(ctx, client, resolved.Endpoint, name, verb, parent)
}

// runToolboxShowWith is the testable core.
func runToolboxShowWith(
	ctx context.Context, client toolboxClient, endpoint, name string,
	verb toolboxShowFlags, parent toolboxFlags,
) error {
	tb, err := client.GetToolbox(ctx, name)
	if err != nil {
		if isAzureNotFound(err) {
			return showPendingOrNotFound(ctx, endpoint, name, verb, parent)
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox)
	}

	shownVersion := verb.version
	if shownVersion == "" {
		shownVersion = tb.DefaultVersion
	}

	version, err := client.GetToolboxVersion(ctx, name, shownVersion)
	if err != nil {
		if isAzureNotFound(err) {
			return exterrors.Dependency(
				exterrors.CodeToolboxNotFound,
				fmt.Sprintf("version %q of toolbox %q not found", shownVersion, name),
				fmt.Sprintf("run 'azd ai agent toolbox show %q' to see the default version", name),
			)
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolboxVersion)
	}

	mcpURL := buildToolboxMcpURL(endpoint, name, shownVersion)

	if parent.output == "json" {
		return emitShowJSON(tb, version, mcpURL)
	}
	return emitShowTable(tb, version, mcpURL)
}

// showPendingOrNotFound handles the 404 branch: either render the pending-toolbox
// view or surface a structured Dependency(CodeToolboxNotFound).
func showPendingOrNotFound(
	ctx context.Context, endpoint, name string,
	verb toolboxShowFlags, parent toolboxFlags,
) error {
	return withAzdClient(func(azdClient *azdext.AzdClient) error {
		pending, err := getPendingToolbox(ctx, azdClient, endpoint, name)
		if err != nil {
			log.Printf("toolbox show: pending-toolbox read failed for %q: %v", name, err)
		}
		if pending == nil {
			return exterrors.Dependency(
				exterrors.CodeToolboxNotFound,
				fmt.Sprintf("toolbox %q not found at %s", name, endpoint),
				"run 'azd ai agent toolbox list' to see available toolboxes",
			)
		}

		return renderPendingShow(name, verb, parent, pending)
	})
}

// renderPendingShow emits the pending-toolbox view.
func renderPendingShow(
	name string, verb toolboxShowFlags, parent toolboxFlags, pending *PendingToolbox,
) error {

	if verb.version != "" {
		return exterrors.Validation(
			exterrors.CodeMissingUpdateField,
			fmt.Sprintf(
				"toolbox %q has no published versions yet; --version cannot be used",
				name,
			),
			fmt.Sprintf(
				"run 'azd ai agent toolbox connection add %q <connection>' to publish v1 first",
				name,
			),
		)
	}

	if parent.output == "json" {
		payload := map[string]any{
			"toolbox": map[string]any{
				"name":        name,
				"pending":     true,
				"description": pending.Description,
				"createdAt":   pending.CreatedAt,
			},
			"version":  nil,
			"endpoint": nil,
		}
		return emitJSON(payload)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")
	fmt.Fprintf(w, "Name\t%s\n", name)
	fmt.Fprintf(w, "State\tpending\n")
	fmt.Fprintf(w, "Description\t%s\n", pending.Description)
	fmt.Fprintf(w, "Created\t%s\n", pending.CreatedAt)
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf(
		"\nRun `azd ai agent toolbox connection add %q <connection>` to publish v1.\n",
		name,
	)
	return nil
}

// buildToolboxMcpURL computes the runtime MCP consumption URL.
// version is service-supplied so both segments are PathEscaped.
func buildToolboxMcpURL(endpoint, name, version string) string {
	return fmt.Sprintf(
		"%s/toolboxes/%s/versions/%s/mcp?api-version=v1",
		strings.TrimRight(endpoint, "/"),
		url.PathEscape(name),
		url.PathEscape(version),
	)
}

// emitShowJSON prints the JSON envelope for `toolbox show`.
func emitShowJSON(
	tb *azure.ToolboxObject, version *azure.ToolboxVersionObject, mcpURL string,
) error {
	return emitJSON(map[string]any{
		"toolbox":  tb,
		"version":  version,
		"endpoint": mcpURL,
	})
}

// emitShowTable renders the table form of `toolbox show`.
func emitShowTable(
	tb *azure.ToolboxObject, version *azure.ToolboxVersionObject, mcpURL string,
) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")
	fmt.Fprintf(w, "Name\t%s\n", tb.Name)
	fmt.Fprintf(w, "Default version\t%s\n", tb.DefaultVersion)
	fmt.Fprintf(w, "Shown version\t%s\n", version.Version)
	fmt.Fprintf(w, "Description\t%s\n", version.Description)
	fmt.Fprintf(w, "Endpoint\t%s\n", mcpURL)
	fmt.Fprintf(w, "Tools\t%d\n", len(version.Tools))
	if err := w.Flush(); err != nil {
		return err
	}

	if len(version.Tools) > 0 {
		fmt.Println()
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "TOOL\tTYPE\tDETAIL")
		fmt.Fprintln(tw, "----\t----\t------")
		for _, tool := range version.Tools {
			toolName, _ := tool["name"].(string)
			toolType, _ := tool["type"].(string)
			detail := describeToolDetail(toolType, tool)
			fmt.Fprintf(tw, "%s\t%s\t%s\n", toolName, toolType, detail)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// describeToolDetail returns the per-tool annotation used in the show table:
// "(builtin)" for first-party tools and "(connection:<id>)" for connection-backed entries.
func describeToolDetail(toolType string, tool map[string]any) string {
	switch toolType {
	case "code_interpreter", "web_search", "file_search":
		return "(builtin)"
	case "mcp", "azure_ai_search":
		if id := firstConnectionID(tool); id != "" {
			return "(connection:" + id + ")"
		}
	}
	return ""
}

// firstConnectionID returns the first project_connection_id referenced by a
// tool entry — top-level on `mcp` tools, or nested under
// azure_ai_search.indexes[] for search tools.
func firstConnectionID(tool map[string]any) string {
	var found string
	toolEntryReferences(tool, func(id string) bool {
		found = id
		return true // stop at the first hit
	})
	return found
}
