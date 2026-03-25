// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func newLogoutCmd(parent string) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out of Azure.",
		Long:  "Log out of Azure",
		Annotations: map[string]string{
			loginCmdParentAnnotation: parent,
		},
	}
}

type logoutAction struct {
	authManager       *auth.Manager
	accountSubManager *account.SubscriptionsManager
	formatter         output.Formatter
	writer            io.Writer
	console           input.Console
	annotations       CmdAnnotations
}

func newLogoutAction(
	authManager *auth.Manager,
	accountSubManager *account.SubscriptionsManager,
	formatter output.Formatter,
	writer io.Writer,
	console input.Console,
	annotations CmdAnnotations) actions.Action {
	return &logoutAction{
		authManager:       authManager,
		accountSubManager: accountSubManager,
		formatter:         formatter,
		writer:            writer,
		console:           console,
		annotations:       annotations,
	}
}

func (la *logoutAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	tracing.SetUsageAttributes(fields.AuthMethodKey.String("logout"))

	if la.annotations[loginCmdParentAnnotation] == "" {
		fmt.Fprintln(
			la.console.Handles().Stderr,
			output.WithWarningFormat(
				"WARNING: `azd logout` is deprecated and will be removed in a future release."))
		fmt.Fprintln(
			la.console.Handles().Stderr,
			"Next time use `azd auth logout`.")
	}

	err := la.authManager.Logout(ctx)
	if err != nil {
		tracing.SetUsageAttributes(fields.AuthResultKey.String("failure"))
		return nil, err
	}

	err = la.accountSubManager.ClearSubscriptions(ctx)
	if err != nil {
		tracing.SetUsageAttributes(fields.AuthResultKey.String("failure"))
		return nil, err
	}

	tracing.SetUsageAttributes(fields.AuthResultKey.String("success"))
	return nil, nil
}
