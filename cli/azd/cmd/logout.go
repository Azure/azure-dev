// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out of Azure",
		Long:  "Log out of Azure",
	}
}

type logoutAction struct {
	authManager *auth.Manager
	formatter   output.Formatter
	writer      io.Writer
}

func newLogoutAction(authManager *auth.Manager, formatter output.Formatter, writer io.Writer) actions.Action {
	return &logoutAction{
		authManager: authManager,
		formatter:   formatter,
		writer:      writer,
	}
}

func (la *logoutAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	return nil, la.authManager.Logout(ctx)
}
