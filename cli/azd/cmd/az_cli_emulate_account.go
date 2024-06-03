package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func azCliEmulateAccountCommands(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("account", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:    "account",
			Short:  "Emulates az account commands",
			Hidden: true,
		},
	})

	group.Add("show", &actions.ActionDescriptorOptions{
		Command:        newAccountShowCmd(),
		FlagsResolver:  newAccountShowFlags,
		ActionResolver: newAccountAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.JsonFormat,
	})

	group.Add("get-access-token", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:    "get-access-token",
			Hidden: true,
		},
		FlagsResolver:  newAuthTokenFlags,
		ActionResolver: newAuthTokenAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.JsonFormat,
	})

	return group
}

type accountShowFlags struct {
	global *internal.GlobalCommandOptions
	internal.EnvFlag
}

func (s *accountShowFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	s.EnvFlag.Bind(local, global)
	s.global = global
}

func newAccountShowFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *accountShowFlags {
	flags := &accountShowFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newAccountShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "show",
		Hidden: true,
	}
}

type accountShowAction struct {
	env        *environment.Environment
	console    input.Console
	subManager *account.SubscriptionsManager
}

func newAccountAction(
	console input.Console,
	env *environment.Environment,
	subManager *account.SubscriptionsManager,
) actions.Action {
	return &accountShowAction{
		console:    console,
		env:        env,
		subManager: subManager,
	}
}

type accountShowOutput struct {
	Id       string `json:"id"`
	TenantId string `json:"tenantId"`
}

func (s *accountShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	subId := s.env.GetSubscriptionId()
	tenantId, err := s.subManager.LookupTenant(ctx, subId)
	if err != nil {
		return nil, err
	}
	o := accountShowOutput{
		Id:       subId,
		TenantId: tenantId,
	}
	output, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	fmt.Fprint(s.console.Handles().Stdout, string(output))
	return nil, nil
}
