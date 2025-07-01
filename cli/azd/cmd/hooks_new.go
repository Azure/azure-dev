// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/tools"
)

func newHooksNewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "new",
		Short: "Create a new hook for the project.",
	}
}

func newHooksNewFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *hooksNewFlags {
	flags := &hooksNewFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

type hooksNewFlags struct {
	internal.EnvFlag
	global   *internal.GlobalCommandOptions
	platform string
	service  string
}

func (f *hooksNewFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global

	local.StringVar(&f.platform, "platform", "", "Forces hooks to run for the specified platform.")
	local.StringVar(&f.service, "service", "", "Only runs hooks for the specified service.")
}

type hooksNewAction struct {
	commandRunner  exec.CommandRunner
	console        input.Console
	flags          *hooksNewFlags
	args           []string
	serviceLocator ioc.ServiceLocator
	llmManager     llm.Manager
}

func newHooksNewAction(
	commandRunner exec.CommandRunner,
	console input.Console,
	flags *hooksNewFlags,
	args []string,
	serviceLocator ioc.ServiceLocator,
	llmManager llm.Manager,
) actions.Action {
	return &hooksNewAction{
		commandRunner:  commandRunner,
		console:        console,
		flags:          flags,
		args:           args,
		serviceLocator: serviceLocator,
		llmManager:     llmManager,
	}
}

func (hna *hooksNewAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	llmInfo, err := hna.llmManager.Info(hna.console.GetWriter())
	if err != nil {
		return nil, fmt.Errorf("failed to load LLM info: %w", err)
	}
	llClient, err := llm.LlmClient(llmInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client: %w", err)
	}

	agent := agents.NewOneShotAgent(llClient, []tools.Tool{
		tools.Calculator{},
	})
	executor := agents.NewExecutor(agent)
	answer, err := chains.Run(ctx, executor, "If I have 4 apples and I give 2 to my friend, how many apples do I have left?",
		chains.WithTemperature(0.0),
		chains.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
			fmt.Fprintf(hna.console.GetWriter(), "%s", chunk)
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to exe: %w", err)
	}
	fmt.Println(answer)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Done",
		},
	}, nil
}
