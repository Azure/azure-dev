// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// toolboxDeleteFlags carries the verb-specific flags for `toolbox delete`.
type toolboxDeleteFlags struct {
	version string
	force   bool
}

// newToolboxDeleteCommand returns the `azd ai toolbox delete <name>` command.
func newToolboxDeleteCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &toolboxDeleteFlags{}

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a toolbox or a single version.",
		Long: `Delete a toolbox or one of its versions.

Without --version the whole toolbox is removed (cascades to every version) and
its TOOLBOX_<NORMALIZED_NAME>_MCP_ENDPOINT value is cleared from the active azd
environment. With --version the named version is deleted; the CLI refuses to
delete the default version while others exist (retarget first) or — without
--force — when it is the only remaining version (which would cascade and remove
the toolbox).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxDelete(cmd.Context(), args[0], *flags, readToolboxFlags(cmd, extCtx))
		},
	}

	cmd.Flags().StringVar(
		&flags.version, "version", "",
		"Delete a single version instead of the whole toolbox.",
	)
	cmd.Flags().BoolVar(
		&flags.force, "force", false,
		"Skip confirmation prompts and override safety checks where allowed.",
	)
	registerToolboxOutputFlag(cmd)

	return cmd
}

func runToolboxDelete(
	ctx context.Context, name string, verb toolboxDeleteFlags, parent toolboxFlags,
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
	logResolvedEndpoint("toolbox delete", resolved)

	return runToolboxDeleteWith(ctx, client, name, verb, parent)
}

// runToolboxDeleteWith is the testable core. It accepts a toolboxClient
// interface so unit tests can drive the branches without an HTTP server.
func runToolboxDeleteWith(
	ctx context.Context, client toolboxClient, name string,
	verb toolboxDeleteFlags, parent toolboxFlags,
) error {
	if verb.version == "" {
		return runDeleteToolbox(ctx, client, name, verb, parent)
	}
	return runDeleteToolboxVersion(ctx, client, name, verb, parent)
}

// runDeleteToolbox handles `toolbox delete <name>` (no --version).
func runDeleteToolbox(
	ctx context.Context, client toolboxClient, name string,
	verb toolboxDeleteFlags, parent toolboxFlags,
) error {
	// Only the parent-toolbox delete prompts for confirmation; --no-prompt
	// without --force is rejected here, not in runDeleteToolboxVersion which
	// does not prompt.
	if parent.noPrompt && !verb.force {
		return exterrors.Validation(
			exterrors.CodeMissingForceFlag,
			"--no-prompt requires --force when deleting a toolbox",
			"add --force to confirm the deletion non-interactively",
		)
	}
	return withAzdClient(func(azdClient *azdext.AzdClient) error {
		_, getErr := client.GetToolbox(ctx, name)
		switch {
		case getErr == nil:
			if !verb.force {
				confirmed, err := confirmToolboxDelete(ctx, azdClient,
					fmt.Sprintf("Delete toolbox %q (cascades to every version)?", name))
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Println("Aborted.")
					return nil
				}
			}
			if err := client.DeleteToolbox(ctx, name); err != nil && !isAzureNotFound(err) {
				return exterrors.ServiceFromAzure(err, exterrors.OpDeleteToolbox)
			}
			// Whole-toolbox delete clears the endpoint; version delete does not.
			if err := setToolboxEndpointEnvFunc(ctx, name, ""); err != nil {
				return err
			}
			return emitDeleteResult(name, "", "deleted", parent.output)

		case isAzureNotFound(getErr):
			return exterrors.Dependency(
				exterrors.CodeToolboxNotFound,
				fmt.Sprintf("toolbox %q not found", name),
				"run 'azd ai toolbox list' to see available toolboxes",
			)

		default:
			return exterrors.ServiceFromAzure(getErr, exterrors.OpGetToolbox)
		}
	})
}

// runDeleteToolboxVersion handles `toolbox delete <name> --version <n>`.
func runDeleteToolboxVersion(
	ctx context.Context, client toolboxClient, name string,
	verb toolboxDeleteFlags, parent toolboxFlags,
) error {
	tb, err := client.GetToolbox(ctx, name)
	if err != nil {
		return toolboxNotFoundOrService(err, name, exterrors.OpGetToolbox)
	}

	cascaded := false
	if verb.version == tb.DefaultVersion {
		versions, err := client.ListToolboxVersions(ctx, name)
		if err != nil {
			return exterrors.ServiceFromAzure(err, exterrors.OpListToolboxVersions)
		}

		if len(versions) > 1 {
			return exterrors.Validation(
				exterrors.CodeDefaultVersionDelete,
				fmt.Sprintf(
					"version %q is the default for toolbox %q and other versions exist",
					verb.version, name,
				),
				"retarget the default with `azd ai toolbox publish <name> <other>` first",
			)
		}

		// Only remaining version → cascading delete; require --force.
		if !verb.force {
			return exterrors.Validation(
				exterrors.CodeOnlyVersionDelete,
				fmt.Sprintf(
					"version %q is the only remaining version of toolbox %q; "+
						"deleting it removes the toolbox", verb.version, name,
				),
				fmt.Sprintf(
					"run `azd ai toolbox delete %q` to delete the toolbox, "+
						"or pass --force to confirm",
					name,
				),
			)
		}
		cascaded = true
	}
	// NOTE: non-default version delete has no confirmation prompt by design.
	// We intentionally do not add one here even without --force.

	if err := client.DeleteToolboxVersion(ctx, name, verb.version); err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpDeleteToolboxVersion)
	}

	if cascaded {
		if parent.output == "json" {
			return emitDeleteResult(name, verb.version, "toolbox_cascaded", parent.output)
		}
		fmt.Printf("Deleted toolbox %s (last version removed).\n", name)
		return nil
	}
	return emitDeleteResult(name, verb.version, "version_deleted", parent.output)
}

// confirmToolboxDelete shows a destructive-action confirmation prompt.
func confirmToolboxDelete(ctx context.Context, azdClient *azdext.AzdClient, message string) (bool, error) {
	resp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      message,
			DefaultValue: new(false),
		},
	})
	if err != nil {
		return false, exterrors.FromPrompt(err, "delete confirmation")
	}
	if resp == nil || resp.Value == nil {
		return false, nil
	}
	return *resp.Value, nil
}

func emitDeleteResult(name, version, outcome, output string) error {
	if output == "json" {
		payload := map[string]any{
			"name":    name,
			"version": version,
			"outcome": outcome,
		}
		return emitJSON(payload)
	}
	switch outcome {
	case "deleted":
		fmt.Printf("Deleted toolbox %s.\n", name)
	case "version_deleted":
		fmt.Printf("Deleted version %s of toolbox %s.\n", version, name)
	}
	return nil
}
