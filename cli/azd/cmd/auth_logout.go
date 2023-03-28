// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
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
	err := la.authManager.Logout(ctx)
	if err != nil {
		return nil, err
	}

	err = la.accountSubManager.ClearSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
