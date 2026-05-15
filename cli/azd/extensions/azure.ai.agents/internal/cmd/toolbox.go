// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// toolboxFlags carries the cross-cutting flags shared by every `toolbox` verb.
type toolboxFlags struct {
	projectEndpoint string
	output          string
	noPrompt        bool
}

// toolboxNamePattern is the validation regex for toolbox and tool names per § 4.2.
var toolboxNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// newToolboxCommand builds the `azd ai agent toolbox` parent.
func newToolboxCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "toolbox",
		Short: "Manage Foundry toolboxes (versioned collections of agent tools).",
		Long: `Manage Foundry toolboxes.

A toolbox is a versioned, named collection of connection-backed tools that
agents reference at run time. Each version is immutable and carries the full
tool list; mutations publish a new version and (after the first POST) require
an explicit update to retarget the default.`,
	}

	// --output and --no-prompt are reserved azd globals and are inherited
	// automatically; only the extension-specific flag is registered here.
	cmd.PersistentFlags().String(
		"project-endpoint", "",
		"Foundry project endpoint URL. When unset, falls back to the active azd "+
			"environment, azd user config, then FOUNDRY_PROJECT_ENDPOINT.",
	)
	// Advertise the toolbox-specific --output allowed values + default on the
	// parent so `azd ai agent toolbox --help` shows them too. Leaf commands
	// re-register on themselves; cobra annotations don't propagate.
	registerToolboxOutputFlag(cmd)

	cmd.AddCommand(newToolboxCreateCommand(extCtx))
	cmd.AddCommand(newToolboxUpdateCommand(extCtx))
	cmd.AddCommand(newToolboxDeleteCommand(extCtx))
	cmd.AddCommand(newToolboxShowCommand(extCtx))
	cmd.AddCommand(newToolboxListCommand(extCtx))
	cmd.AddCommand(newToolboxConnectionCommand(extCtx))

	return cmd
}

// readToolboxFlags extracts the persistent flag values from a subcommand. The
// reserved azd globals `--output` and `--no-prompt` come from extCtx.
func readToolboxFlags(cmd *cobra.Command, extCtx *azdext.ExtensionContext) toolboxFlags {
	pe, _ := cmd.Flags().GetString("project-endpoint")
	out := ""
	np := false
	if extCtx != nil {
		out = extCtx.OutputFormat
		np = extCtx.NoPrompt
	}
	return toolboxFlags{projectEndpoint: pe, output: out, noPrompt: np}
}

// validateOutputFormat returns a structured error when --output is not table/json.
// The azd host normally enforces this via RegisterFlagOptions; the check stays
// for direct `azd x` invocation and for unit-test reach.
func validateOutputFormat(out string) error {
	switch strings.ToLower(out) {
	case "", "table", "json":
		return nil
	default:
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid --output value %q", out),
			"use table or json",
		)
	}
}

// registerToolboxOutputFlag attaches the --output annotations every toolbox
// leaf command shares. RegisterFlagOptions writes per-command annotations, so
// it must run on each leaf rather than the parent.
func registerToolboxOutputFlag(cmd *cobra.Command) {
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"table", "json"},
		Default:       "table",
	})
}

// validateToolboxName enforces ^[A-Za-z0-9_-]+$ on the positional `<name>`.
func validateToolboxName(name string) error {
	if !toolboxNamePattern.MatchString(name) {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			fmt.Sprintf("toolbox name %q is invalid", name),
			"names must match ^[A-Za-z0-9_-]+$",
		)
	}
	return nil
}

// validateToolName enforces the same regex on tool-entry names. Failing here
// avoids a service round trip that would yield a generic 400.
func validateToolName(name string) error {
	if !toolboxNamePattern.MatchString(name) {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			fmt.Sprintf(
				"tool entry name %q is invalid; the Foundry service requires names "+
					"to match ^[A-Za-z0-9_-]+$",
				name,
			),
			"rename the project connection so its short name matches the regex",
		)
	}
	return nil
}

// resolveToolboxAndClient walks the endpoint cascade, validates flags, and
// returns a toolbox client bound to the resolved endpoint.
func resolveToolboxAndClient(
	ctx context.Context, flags toolboxFlags,
) (toolboxClient, *resolvedEndpoint, error) {
	if err := validateOutputFormat(flags.output); err != nil {
		return nil, nil, err
	}
	resolved, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{FlagValue: flags.projectEndpoint})
	if err != nil {
		return nil, nil, err
	}
	client, err := newToolboxClient(resolved.Endpoint)
	if err != nil {
		return nil, nil, err
	}
	return client, resolved, nil
}

// isAzureNotFound reports whether err originates from an Azure response with HTTP 404.
func isAzureNotFound(err error) bool {
	if err == nil {
		return false
	}
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return respErr.StatusCode == http.StatusNotFound
	}
	return false
}
