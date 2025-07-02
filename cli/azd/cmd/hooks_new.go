// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

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
		hookResolverTool{},
		osResolverTool{},
		saveHookTool{},
	})
	executor := agents.NewExecutor(agent)
	answer, err := chains.Run(ctx, executor, `
You are an expert in creating hooks for the Azure Dev CLI.
Your task is to create a new hook for linux bash or windows powershell, depending on the user's platform.
Use the os resolver tool to determine the user's platform. You will write a powershell script if the user is on windows,
or a bash script if the user is on linux.
Start by resolving the type of the hook based on the input.
The hook should start with a comment on the top that describes the hook type.
Then use the next prompt to create the hook code:
Ask user for their age and prints how many days they have lived.

Use the save hook tool to save the generated hook to a file.
`,
		chains.WithTemperature(0.0),
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

type hookResolverTool struct {
}

func (h hookResolverTool) Name() string {
	return "Hook Resolver"
}

func (h hookResolverTool) Description() string {
	return `Useful for resolving the type of the hook based on the input.
	The input to this tool should be a string that contains the prompt that creates the hook.`
}

func (h hookResolverTool) Call(ctx context.Context, input string) (string, error) {
	validHookTypes := []string{"preprovision", "postprovision", "predeploy", "postdeploy"}
	for _, hookType := range validHookTypes {
		if strings.Contains(input, hookType) {
			return hookType, nil
		}
	}
	return "preprovision", nil
}

type osResolverTool struct {
}

func (h osResolverTool) Name() string {
	return "Os Resolver"
}

func (h osResolverTool) Description() string {
	return "Useful for resolving what is the user's operating system."
}

func (h osResolverTool) Call(ctx context.Context, input string) (string, error) {
	return runtime.GOOS, nil
}

type saveHookTool struct {
}

func (h saveHookTool) Name() string {
	return "Save Hook"
}

func (h saveHookTool) Description() string {
	return `Useful for saving the generated hook to a file.
    The input to this tool should be a JSON string with the following format:
	{
		"hookType": "<hook type>",
		"hookCode": "<hook code>"
	}.
	The input must be just the JSON string, without any additional text.`
}

func (h saveHookTool) Call(ctx context.Context, input string) (string, error) {
	// Parse the input JSON string
	var hookData struct {
		HookType string `json:"hookType"`
		HookCode string `json:"hookCode"`
	}
	if err := json.Unmarshal([]byte(input), &hookData); err != nil {
		return "", fmt.Errorf("failed to parse input JSON: %w", err)
	}

	// Save the hook code to a file
	if err := os.WriteFile(fmt.Sprintf("%s_hook.sh", hookData.HookType), []byte(hookData.HookCode), 0755); err != nil {
		return "", fmt.Errorf("failed to save hook file: %w", err)
	}

	return "Hook saved successfully", nil
}
