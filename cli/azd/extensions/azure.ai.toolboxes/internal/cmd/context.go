// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package cmd implements the cobra command tree for the azure.ai.toolboxes extension.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry"
	"azure.ai.toolboxes/internal/foundry/projectctx"
	"azure.ai.toolboxes/internal/pkg/azure"

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

// toolboxNamePattern is the validation regex for toolbox and tool names.
var toolboxNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// maxToolboxNameLength caps positional names.
const maxToolboxNameLength = 63

// readToolboxFlags extracts the persistent flag values from a subcommand. The
// reserved azd globals `--output` and `--no-prompt` come from extCtx. `output`
// is normalized to lowercase so downstream branches can compare with `== "json"`.
func readToolboxFlags(cmd *cobra.Command, extCtx *azdext.ExtensionContext) toolboxFlags {
	pe, _ := cmd.Flags().GetString("project-endpoint")
	out := ""
	np := false
	if extCtx != nil {
		out = strings.ToLower(extCtx.OutputFormat)
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

// validateToolboxName enforces ^[A-Za-z0-9_-]+$ on the positional `<name>` and
// caps length at maxToolboxNameLength.
func validateToolboxName(name string) error {
	if !toolboxNamePattern.MatchString(name) || len(name) > maxToolboxNameLength {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			fmt.Sprintf("toolbox name %q is invalid", name),
			fmt.Sprintf("names must match ^[A-Za-z0-9_-]+$ and be at most %d characters", maxToolboxNameLength),
		)
	}
	return nil
}

// validateToolName enforces the same regex on tool-entry names. Failing here
// avoids a service round trip that would yield a generic 400.
func validateToolName(name string) error {
	if !toolboxNamePattern.MatchString(name) || len(name) > maxToolboxNameLength {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			fmt.Sprintf(
				"tool entry name %q is invalid; the Foundry service requires names "+
					"to match ^[A-Za-z0-9_-]+$ (max %d characters)",
				name, maxToolboxNameLength,
			),
			"rename the tool entry (or the underlying project connection) so the name matches the regex",
		)
	}
	return nil
}

// newToolboxClient builds a FoundryToolboxClient bound to the resolved endpoint.
func newToolboxClient(endpoint string) (*azure.FoundryToolboxClient, error) {
	cred, err := foundry.NewCredential()
	if err != nil {
		return nil, err
	}
	return azure.NewFoundryToolboxClient(endpoint, cred), nil
}

// resolveToolboxAndClient walks the endpoint cascade, validates flags, and
// returns a toolbox client bound to the resolved endpoint.
func resolveToolboxAndClient(
	ctx context.Context, flags toolboxFlags,
) (toolboxClient, *projectctx.Resolved, error) {
	if err := validateOutputFormat(flags.output); err != nil {
		return nil, nil, err
	}
	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{FlagValue: flags.projectEndpoint})
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

// logResolvedEndpoint records the resolved endpoint and source to --debug.
func logResolvedEndpoint(verb string, r *projectctx.Resolved) {
	if r == nil {
		return
	}
	log.Printf("%s: resolved project endpoint %s (source=%s)", verb, r.Endpoint, r.Source)
}

// ensureExtensionContext returns a non-nil [azdext.ExtensionContext] so command
// constructors can be safely invoked from tests with a nil receiver.
func ensureExtensionContext(extCtx *azdext.ExtensionContext) *azdext.ExtensionContext {
	if extCtx == nil {
		return &azdext.ExtensionContext{}
	}
	return extCtx
}
