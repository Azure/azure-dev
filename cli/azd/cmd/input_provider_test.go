// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

type inputTestAuth struct{}

func (inputTestAuth) Cloud() *cloud.Cloud { return cloud.AzurePublic() }
func (inputTestAuth) Mode() (auth.AuthSource, error) {
	return auth.AzdBuiltIn, nil
}
func (inputTestAuth) CredentialForCurrentUser(
	context.Context, *auth.CredentialForCurrentUserOptions,
) (azcore.TokenCredential, error) {
	return &mocks.MockCredentials{}, nil
}

type inputTestPrincipal struct{}

func (inputTestPrincipal) CurrentPrincipalId(context.Context) (string, error) {
	return "11111111-1111-1111-1111-111111111111", nil
}
func (inputTestPrincipal) CurrentPrincipalType(context.Context) (provisioning.PrincipalType, error) {
	return provisioning.UserType, nil
}

func TestBetaInputProviderCommands(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZD_FORCE_TTY", "false")
	t.Setenv(environment.EnvNameEnvVarName, "")
	t.Setenv(environment.SubscriptionIdEnvVarName, "00000000-0000-0000-0000-000000000000")
	t.Setenv("AZD_BICEP_TOOL_PATH", "input-test-bicep")

	demoBinary := "demo"
	if runtime.GOOS == "windows" {
		demoBinary += ".exe"
	}
	moduleDir, err := filepath.Abs("..")
	require.NoError(t, err)
	buildCtx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	//nolint:gosec // G204: fixed source target and test-owned output directory.
	build := osexec.CommandContext(buildCtx, "go", "build",
		"-o", filepath.Join(configDir, demoBinary), "./extensions/microsoft.azd.demo")
	build.Dir = moduleDir
	buildOutput, err := build.CombinedOutput()
	require.NoError(t, err, "%s", buildOutput)

	configJSON := `{"extension":{"installed":{"microsoft.azd.demo":{
		"id":"microsoft.azd.demo","namespace":"demo","version":"0.8.0",
		"path":"` + demoBinary + `","capabilities":["lifecycle-events","input-provider",
		"service-target-provider","framework-service-provider","provisioning-provider","validation-provider"],
		"providers":[{"name":"demo","type":"service-target"},{"name":"demo","type":"provisioning-provider"}]
	}}}}`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.json"), []byte(configJSON), 0600))

	for _, needsLocation := range []bool{false, true} {
		t.Run(map[bool]string{false: "no location input", true: "required location"}[needsLocation], func(t *testing.T) {
			projectDir := t.TempDir()
			t.Chdir(projectDir)
			t.Setenv(environment.LocationEnvVarName, "")
			require.NoError(t, os.Unsetenv(environment.LocationEnvVarName))
			require.NoError(t, os.WriteFile("azure.yaml", []byte(
				"name: inputs-test\nworkflows:\n  up:\n    steps:\n      - azd: env get-values\n",
			), 0600))
			require.NoError(t, os.Mkdir("infra", 0700))
			source := "targetScope = 'subscription'\n"
			parameters := `{}`
			if needsLocation {
				source += "param location string\n"
				parameters = `{"location":{"type":"string"}}`
				require.NoError(t, os.WriteFile(filepath.Join("infra", "main.parameters.json"),
					[]byte(`{"parameters":{"location":{"value":"${AZURE_LOCATION}"}}}`), 0600))
			}
			require.NoError(t, os.WriteFile(filepath.Join("infra", "main.bicep"), []byte(source), 0600))
			compiled := `{"$schema":` +
				`"https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",` +
				`"contentVersion":"1.0.0.0","parameters":` + parameters + `,"resources":[]}`

			container := ioc.NewNestedContainer(nil)
			ioc.RegisterInstance(container, &internal.GlobalCommandOptions{NoPrompt: true})
			ioc.RegisterInstance(container, t.Context())
			root := NewRootCmd(false, nil, container)
			container.MustRegisterScoped(func() middleware.CurrentUserAuthManager { return inputTestAuth{} })
			ioc.RegisterInstance[account.Manager](container, &mockaccount.MockAccountManager{
				Locations: []account.Location{{Name: "eastus2"}},
			})
			container.MustRegisterScoped(func() provisioning.CurrentPrincipalIdProvider { return inputTestPrincipal{} })
			var output installProgressBuffer
			runner := mockexec.NewMockCommandRunner()
			realRunner := exec.NewCommandRunner(&exec.RunnerOptions{
				Stdout: &output, Stderr: &output, Stdin: bytes.NewReader(nil),
			})
			runner.When(func(exec.RunArgs, string) bool { return true }).RespondContextFn(realRunner.Run)
			runner.When(func(args exec.RunArgs, _ string) bool {
				return args.Cmd == "input-test-bicep"
			}).Respond(exec.RunResult{Stdout: compiled})
			ioc.RegisterInstance[exec.CommandRunner](container, runner)
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs([]string{"up", "--no-prompt"})
			ctx, stop := context.WithTimeout(t.Context(), 20*time.Second)
			defer stop()
			require.NoError(t, root.ExecuteContext(ctx), "%s", output.String())
			require.Contains(t, output.String(),
				`Resolved input "environment.name" from extensions: microsoft.azd.demo (unique recommendation).`)

			dotenv, err := os.ReadFile(filepath.Join(projectDir, ".azure", "demo", ".env"))
			require.NoError(t, err)
			require.Contains(t, string(dotenv), `AZURE_ENV_NAME="demo"`)
			if needsLocation {
				require.Contains(t, string(dotenv), `AZURE_LOCATION="eastus2"`)
				require.Contains(t, output.String(),
					`Resolved input "location" from extensions: microsoft.azd.demo (only valid option).`)
			} else {
				require.NotContains(t, string(dotenv), environment.LocationEnvVarName)
				require.NotContains(t, output.String(), `Resolved input "location"`)
			}
		})
	}

	for _, tt := range []struct {
		name        string
		args        []string
		envName     string
		processName string
		starts      int32
		interactive bool
	}{
		{name: "init template", args: []string{"init"}, envName: "demo", starts: 1},
		{name: "interactive init template", args: []string{"init"}, envName: "demo", starts: 1, interactive: true},
		{name: "init explicit environment", args: []string{"init", "--environment", "explicit"},
			envName: "explicit"},
		{name: "init process environment", args: []string{"init"}, envName: "from-process", processName: "from-process"},
		{name: "init flag wins over process", args: []string{"init", "--environment", "explicit"},
			envName: "explicit", processName: "from-process"},
		{name: "env new", args: []string{"env", "new"}, envName: "demo", starts: 1},
		{name: "env new explicit environment", args: []string{"env", "new", "explicit"}, envName: "explicit"},
		{name: "version", args: []string{"version"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(environment.EnvNameEnvVarName, tt.processName)
			projectDir := t.TempDir()
			t.Chdir(projectDir)
			args := slices.Clone(tt.args)
			if args[0] == "init" {
				templateDir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(templateDir, "azure.yaml"),
					[]byte("name: inputs-test\n"), 0600))
				args = append(args, "--template", templateDir, ".")
			} else if tt.envName != "" {
				require.NoError(t, os.WriteFile("azure.yaml", []byte("name: inputs-test\n"), 0600))
			}

			container := ioc.NewNestedContainer(nil)
			ioc.RegisterInstance(container, &internal.GlobalCommandOptions{NoPrompt: !tt.interactive})
			ioc.RegisterInstance(container, t.Context())
			root := NewRootCmd(false, nil, container)
			var output installProgressBuffer
			realRunner := exec.NewCommandRunner(&exec.RunnerOptions{
				Stdout: &output, Stderr: &output, Stdin: bytes.NewReader(nil),
			})
			runner := mockexec.NewMockCommandRunner()
			var starts atomic.Int32
			runner.When(func(exec.RunArgs, string) bool { return true }).RespondContextFn(
				func(ctx context.Context, args exec.RunArgs) (exec.RunResult, error) {
					if len(args.Args) > 0 && args.Args[0] == "listen" {
						starts.Add(1)
					}
					return realRunner.Run(ctx, args)
				},
			)
			ioc.RegisterInstance[exec.CommandRunner](container, runner)
			root.SetOut(&output)
			root.SetErr(&output)
			if !tt.interactive {
				args = append(args, "--no-prompt")
			}
			root.SetArgs(args)
			ctx, stop := context.WithTimeout(t.Context(), 20*time.Second)
			defer stop()
			require.NoError(t, root.ExecuteContext(ctx), "%s", output.String())
			require.Equal(t, tt.starts, starts.Load(), "%s", output.String())
			if tt.envName != "" {
				dotenv, err := os.ReadFile(filepath.Join(projectDir, ".azure", tt.envName, ".env"))
				require.NoError(t, err)
				require.Contains(t, string(dotenv), `AZURE_ENV_NAME="`+tt.envName+`"`)
			}
			if tt.starts > 0 {
				require.Contains(t, output.String(),
					`Resolved input "environment.name" from extensions: microsoft.azd.demo (unique recommendation).`)
			} else {
				require.NotContains(t, output.String(), "Resolved input ")
			}
		})
	}
}
