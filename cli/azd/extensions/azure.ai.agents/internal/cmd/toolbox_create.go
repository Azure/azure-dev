// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// toolboxCreateFlags holds the verb-specific flags for `toolbox create`.
type toolboxCreateFlags struct {
	description string
}

// newToolboxCreateCommand returns the `azd ai agent toolbox create <name>` command.
// `create` records a local pending entry; v1 is POSTed on the first
// `connection add`.
func newToolboxCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &toolboxCreateFlags{}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Register a new toolbox locally (publishes on first `connection add`).",
		Long: `Register a new toolbox locally.

A toolbox must have at least one tool before it can be published, so 'create'
only records a local pending entry. The first 'connection add' against the
same toolbox name publishes v1 and clears the pending record.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxCreate(cmd.Context(), args[0], *flags, readToolboxFlags(cmd, extCtx))
		},
	}

	cmd.Flags().StringVar(
		&flags.description, "description", "",
		"Optional description recorded with the toolbox.",
	)
	registerToolboxOutputFlag(cmd)

	return cmd
}

func runToolboxCreate(
	ctx context.Context, name string, verb toolboxCreateFlags, parent toolboxFlags,
) error {
	if err := validateToolboxName(name); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	resolved, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{FlagValue: parent.projectEndpoint})
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox create", resolved)

	// Check whether the toolbox already exists on the service.
	client, err := newToolboxClient(resolved.Endpoint)
	if err != nil {
		return err
	}

	if _, err := client.GetToolbox(ctx, name); err == nil {
		return emitCreateResult(name, true /* alreadyExists */, parent.output, verb, resolved.Endpoint)
	} else if !isAzureNotFound(err) {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox)
	}

	// New name → record a pending entry.
	if err := withAzdClient(func(azdClient *azdext.AzdClient) error {
		record := PendingToolbox{
			Description: verb.description,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		}
		if err := setPendingToolbox(ctx, azdClient, resolved.Endpoint, name, record); err != nil {
			return exterrors.Internal(exterrors.CodePendingToolboxStoreFailed, err.Error())
		}
		return nil
	}); err != nil {
		return err
	}

	return emitCreateResult(name, false, parent.output, verb, resolved.Endpoint)
}

// emitCreateResult prints the standard one-liner or JSON envelope.
func emitCreateResult(
	name string, alreadyExists bool, output string, verb toolboxCreateFlags, endpoint string,
) error {
	if output == "json" {
		payload := map[string]any{
			"toolbox": map[string]any{
				"name":        name,
				"pending":     !alreadyExists,
				"description": verb.description,
			},
			"endpoint":      endpoint,
			"alreadyExists": alreadyExists,
		}
		return emitJSON(payload)
	}

	if alreadyExists {
		fmt.Printf("Toolbox %s already exists.\n", name)
		fmt.Println("Next steps:")
		fmt.Println("  - Run 'azd ai agent toolbox connection add' to publish a new version.")
		fmt.Println("  - Run 'azd ai agent toolbox update --default-version <n>' to retarget the default.")
		return nil
	}
	fmt.Printf("Registered toolbox %s (pending tools).\n", name)
	fmt.Println("Next step:")
	fmt.Printf("  Run 'azd ai agent toolbox connection add %s <connection>' to publish v1.\n", name)
	return nil
}
