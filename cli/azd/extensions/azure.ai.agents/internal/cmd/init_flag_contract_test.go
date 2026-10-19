// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestInitFlagContract(t *testing.T) {
	modes := []Mode{ModeHostedFromCode, ModePrompt, ModePromptVoice, ModeTemplateAdopt, ModeImage}
	cmd := newInitCommand(&azdext.ExtensionContext{NoPrompt: true})
	specs := map[string]FlagSpec{}
	for _, spec := range Contract() {
		require.NotContains(t, specs, spec.Name, "duplicate contract flag")
		specs[spec.Name] = spec
		flag := cmd.Flags().Lookup(spec.Name)
		require.NotNil(t, flag, "stale contract entry: %s", spec.Name)
		require.NotEmpty(t, spec.Help, spec.Name)
		require.Equal(t, spec.Help, flag.Usage)
		defaultValue := flag.DefValue
		if defaultValue == "false" || defaultValue == "[]" {
			defaultValue = ""
		}
		require.Equal(t, spec.Default, defaultValue, spec.Name)
		require.NotEmpty(t, spec.Modes, spec.Name)
		for _, mode := range spec.Modes {
			require.Contains(t, modes, mode, spec.Name)
		}
	}
	// Include hidden harness and deprecated manifest: neither may escape the contract.
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		require.Contains(t, specs, flag.Name, "untracked local flag")
		for _, mode := range modes {
			t.Run(string(mode)+"/"+flag.Name, func(t *testing.T) {
				spec := specs[flag.Name]
				allowed := AllowedIn(mode, flag.Name)
				require.Equal(t, slices.Contains(spec.Modes, mode), allowed)
				for _, value := range []string{"value", ""} {
					command := newInitCommand(&azdext.ExtensionContext{NoPrompt: true})
					if flag.Value.Type() == "bool" {
						if value == "" {
							value = "false"
						} else {
							value = "true"
						}
					}
					require.NoError(t, command.Flags().Set(flag.Name, value))
					err := ValidateModeFlags(command, mode)
					if allowed {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, "--"+flag.Name)
						localErr, ok := errors.AsType[*azdext.LocalError](err)
						require.True(t, ok)
						require.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
					}
				}
			})
		}
	})
}

func TestResolveModeFromFlags(t *testing.T) {
	for _, test := range []struct {
		name     string
		template bool
		kind     string
		image    string
		want     Mode
	}{
		{name: "default", want: ModeHostedFromCode},
		{name: "explicit hosted", kind: "hosted", want: ModeHostedFromCode},
		{name: "prompt", kind: "prompt", want: ModePrompt},
		{name: "normalized voice", kind: " Prompt-Voice ", want: ModePromptVoice},
		{name: "image", image: "example.com/agent:v1", want: ModeImage},
		{name: "prompt before image", kind: "prompt", image: "image", want: ModePrompt},
		{name: "voice before image", kind: "prompt-voice", image: "image", want: ModePromptVoice},
		{name: "template first", template: true, kind: "prompt-voice", image: "image", want: ModeTemplateAdopt},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, ResolveModeFromFlags(test.template, test.kind, test.image))
		})
	}
}

func TestInitHarnessContract(t *testing.T) {
	cmd := newInitCommand(nil)
	require.Contains(t, cmd.Flags().Lookup("harness").Usage,
		"'microsoft_copilot_preview' (Microsoft Copilot Brain+Hand)")
	for _, spec := range Contract() {
		if spec.Name != "harness" {
			continue
		}
		require.ElementsMatch(t, []string{
			agent_api.ManagedAgentHarnessMicrosoftCopilot,
			agent_api.ManagedAgentHarnessGitHubCopilot,
		}, spec.AllowedValues)
		for _, value := range spec.AllowedValues {
			require.NoError(t, validateInitKindHarness(
				AgentKindChoicePrompt, "prompt", value, false))
		}
	}
}

func TestInitContractIgnoresGlobalsAndRejectsUntrackedLocalFlags(t *testing.T) {
	root := &cobra.Command{Use: "agent"}
	root.PersistentFlags().String("future-global", "", "")
	cmd := newInitCommand(nil)
	root.AddCommand(cmd)
	require.NoError(t, cmd.ParseFlags([]string{"--future-global=value"}))
	for _, name := range []string{
		"cwd", "environment", "env", "no-prompt", "output", "debug", "help", "docs",
		"trace-log-file", "trace-log-url",
	} {
		cmd.Flags().String(name, "", "")
		require.NoError(t, cmd.Flags().Set(name, "value"))
	}
	require.NoError(t, ValidateModeFlags(cmd, ModePromptVoice))
	cmd.Flags().String("untracked", "", "")
	require.NoError(t, cmd.Flags().Set("untracked", ""))
	require.Equal(t, []string{"--untracked"}, RejectedFlags(cmd, ModePromptVoice))
	require.ErrorContains(t, ValidateModeFlags(cmd, ModePromptVoice), "--untracked")
	ApplyFlagHelp(&cobra.Command{}) // Missing registrations are deliberately skipped.
}

func TestInitContractRejectsChangedFlagsInteractively(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Chdir(t.TempDir())
	cmd := newInitCommand(nil)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--kind=prompt", "--runtime=", "--deploy-mode=code"})
	err := cmd.Execute()
	require.ErrorContains(t, err, "prompt init")
	require.ErrorContains(t, err, "--runtime")
	require.ErrorContains(t, err, "--deploy-mode")
	require.Equal(t, []string{"--deploy-mode", "--runtime"}, RejectedFlags(cmd, ModePrompt))
}

func TestInitContractRejectsPromptOnlyFlagsInteractivelyWithoutKind(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, test := range []struct {
		flag  string
		value string
	}{
		{flag: "description", value: "My agent"},
		{flag: "rai-policy", value: "none"},
		{flag: "instructions", value: "Be helpful"},
		{flag: "harness", value: "github_copilot_preview"},
	} {
		for _, value := range []string{test.value, ""} {
			t.Run(test.flag+"/"+value, func(t *testing.T) {
				t.Chdir(t.TempDir())
				cmd := newInitCommand(nil)
				var output bytes.Buffer
				cmd.SetOut(&output)
				cmd.SetErr(&output)
				cmd.SetArgs([]string{"--" + test.flag + "=" + value})
				err := cmd.Execute()
				require.ErrorContains(t, err, "--"+test.flag)
				require.ErrorContains(t, err, "hosted init cannot apply")
				require.NotContains(t, err.Error(), "hosted-from-code")
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
				require.NoFileExists(t, "azure.yaml")
				require.NoDirExists(t, ".azure")
			})
		}
	}
}

func TestValidateModeFlagsUsesFriendlyModeLabels(t *testing.T) {
	for _, test := range []struct {
		mode  Mode
		label string
	}{
		{mode: ModeHostedFromCode, label: "hosted"},
		{mode: ModeImage, label: "image"},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			cmd := newInitCommand(nil)
			require.NoError(t, cmd.Flags().Set("description", ""))
			err := ValidateModeFlags(cmd, test.mode)
			require.ErrorContains(t, err, test.label+" init cannot apply")
			require.ErrorContains(t, err, "--description")
			require.NotContains(t, err.Error(), "hosted-from-code")
		})
	}
}

func TestInitModeContractRejectsBeforeHostAccess(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, test := range []struct {
		name string
		args []string
		flag string
	}{
		{name: "prompt runtime", args: []string{"--kind=prompt", "--runtime=python_3_13"}, flag: "--runtime"},
		{name: "prompt image", args: []string{"--kind=prompt", "--image=example.com/agent:v1"}, flag: "--image"},
		{name: "prompt empty src", args: []string{"--kind=prompt", "--src="}, flag: "--src"},
		{name: "prompt positional src", args: []string{"--kind=prompt", "./source"}, flag: "--src"},
		{name: "prompt force false", args: []string{"--kind=prompt", "--force=false"}, flag: "--force"},
		{name: "prompt infra", args: []string{"--kind=prompt", "--infra=bicep"}, flag: "--infra"},
		{name: "hosted description", args: []string{"--kind=hosted", "--description="}, flag: "--description"},
		{name: "headless default policy", args: []string{"--rai-policy=none"}, flag: "--rai-policy"},
		{name: "image runtime",
			args: []string{"--image=example.com/agent:v1", "--agent-name=agent", "--runtime="}, flag: "--runtime"},
		{name: "image acr",
			args: []string{"--image=example.com/agent:v1", "--agent-name=agent", "--acr-connection="},
			flag: "--acr-connection"},
		{name: "template kind", args: []string{"--template=azure.yaml", "--kind=prompt"}, flag: "--kind"},
		{name: "manifest description", args: []string{"--manifest=azure.yaml", "--description="}, flag: "--description"},
		{name: "positional template", args: []string{"azure.yaml", "--protocol=responses"}, flag: "--protocol"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			cmd := newInitCommand(&azdext.ExtensionContext{NoPrompt: true})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(test.args)
			err := cmd.Execute()
			require.ErrorContains(t, err, test.flag)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
			require.NoFileExists(t, "azure.yaml")
			require.NoDirExists(t, ".azure")
			require.NoDirExists(t, "source")
		})
	}
}
