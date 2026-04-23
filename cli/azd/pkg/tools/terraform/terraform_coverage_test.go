// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terraform

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Name_ReturnsTerraformCli(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	require.Equal(t, "Terraform CLI", cli.Name())
}

func Test_InstallUrl_ReturnsExpectedUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	require.Equal(t, "https://aka.ms/azure-dev/terraform-install", cli.InstallUrl())
}

func Test_VersionInfo_ReturnsMinimumVersion(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	info := cli.versionInfo()
	require.Equal(t, uint64(1), info.MinimumVersion.Major)
	require.Equal(t, uint64(1), info.MinimumVersion.Minor)
	require.Equal(t, uint64(7), info.MinimumVersion.Patch)
	require.Contains(t, info.UpdateCommand, "terraform.io/downloads")
}

func Test_CheckInstalled(t *testing.T) {
	validVersionJSON := `{"terraform_version":"1.5.0","platform":"linux_amd64"}`

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", nil)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, validVersionJSON, ""))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.NoError(t, err)
	})

	t.Run("ExactMinimumVersion", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", nil)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"terraform_version":"1.1.7"}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.NoError(t, err)
	})

	t.Run("NotInPath", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", errors.New("terraform not found in PATH"))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)
		require.Contains(t, err.Error(), "terraform not found in PATH")
	})

	t.Run("VersionCommandFails", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", nil)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).SetError(fmt.Errorf("command execution failed"))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)
		require.Contains(t, err.Error(), "checking Terraform CLI version")
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", nil)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, "this is not json", ""))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)
		require.Contains(t, err.Error(), "checking Terraform CLI version")
	})

	t.Run("MissingVersionComponent", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", nil)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"platform":"linux_amd64"}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)
		require.Contains(t, err.Error(), "checking Terraform CLI version")
	})

	t.Run("NonStringVersionComponent", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", nil)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"terraform_version":123}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)
		require.Contains(t, err.Error(), "checking Terraform CLI version")
	})

	t.Run("InvalidSemver", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", nil)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"terraform_version":"not-a-version"}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)
		require.Contains(t, err.Error(), "converting to semver version fails")
	})

	t.Run("VersionTooOld", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", nil)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"terraform_version":"1.0.0"}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)

		var semverErr *tools.ErrSemver
		require.True(t, errors.As(err, &semverErr))
		require.Equal(t, "Terraform CLI", semverErr.ToolName)
	})

	t.Run("VersionJustBelowMinimum", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.MockToolInPath("terraform", nil)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"terraform_version":"1.1.6"}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		err := cli.CheckInstalled(*mockContext.Context)
		require.Error(t, err)

		var semverErr *tools.ErrSemver
		require.True(t, errors.As(err, &semverErr))
	})
}

func Test_UnmarshalCliVersion(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"terraform_version":"1.5.0","platform":"linux_amd64"}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		version, err := cli.unmarshalCliVersion(*mockContext.Context, "terraform_version")
		require.NoError(t, err)
		require.Equal(t, "1.5.0", version)
	})

	t.Run("DifferentComponent", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"terraform_version":"1.5.0","platform":"linux_amd64"}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		version, err := cli.unmarshalCliVersion(*mockContext.Context, "platform")
		require.NoError(t, err)
		require.Equal(t, "linux_amd64", version)
	})

	t.Run("CommandError", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).SetError(fmt.Errorf("terraform not available"))

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.unmarshalCliVersion(*mockContext.Context, "terraform_version")
		require.Error(t, err)
		require.Contains(t, err.Error(), "terraform not available")
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, "{invalid", ""))

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.unmarshalCliVersion(*mockContext.Context, "terraform_version")
		require.Error(t, err)
	})

	t.Run("MissingComponent", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"terraform_version":"1.5.0"}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.unmarshalCliVersion(*mockContext.Context, "nonexistent_key")
		require.Error(t, err)
		require.Contains(t, err.Error(), "reading Terraform CLI component 'nonexistent_key' version failed")
	})

	t.Run("NonStringComponent", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).Respond(exec.NewRunResult(0, `{"terraform_version":42}`, ""))

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.unmarshalCliVersion(*mockContext.Context, "terraform_version")
		require.Error(t, err)
		require.Contains(t, err.Error(), "reading Terraform CLI component 'terraform_version' version failed")
	})
}

// commandTestCase defines a terraform subcommand and its expected behavior.
type commandTestCase struct {
	name         string
	invoke       func(*Cli, context.Context) (string, error)
	expectedArgs []string
	interactive  bool
	errContains  string
}

func getCommandTestCases() []commandTestCase {
	return []commandTestCase{
		{
			name: "Validate",
			invoke: func(cli *Cli, ctx context.Context) (string, error) {
				return cli.Validate(ctx, "/module")
			},
			expectedArgs: []string{"-chdir=/module", "validate"},
			interactive:  false,
			errContains:  "failed running terraform validate",
		},
		{
			name: "Init",
			invoke: func(cli *Cli, ctx context.Context) (string, error) {
				return cli.Init(ctx, "/module")
			},
			expectedArgs: []string{"-chdir=/module", "init", "-upgrade"},
			interactive:  true,
			errContains:  "failed running terraform init",
		},
		{
			name: "Plan",
			invoke: func(cli *Cli, ctx context.Context) (string, error) {
				return cli.Plan(ctx, "/module", "/plan.out")
			},
			expectedArgs: []string{"-chdir=/module", "plan", "-out=/plan.out", "-lock=false"},
			interactive:  true,
			errContains:  "failed running terraform plan",
		},
		{
			name: "Apply",
			invoke: func(cli *Cli, ctx context.Context) (string, error) {
				return cli.Apply(ctx, "/module")
			},
			expectedArgs: []string{"-chdir=/module", "apply", "-lock=false"},
			interactive:  true,
			errContains:  "failed running terraform apply",
		},
		{
			name: "Output",
			invoke: func(cli *Cli, ctx context.Context) (string, error) {
				return cli.Output(ctx, "/module")
			},
			expectedArgs: []string{"-chdir=/module", "output", "-json"},
			interactive:  false,
			errContains:  "failed running terraform output",
		},
		{
			name: "Show",
			invoke: func(cli *Cli, ctx context.Context) (string, error) {
				return cli.Show(ctx, "/module")
			},
			expectedArgs: []string{"-chdir=/module", "show", "-json"},
			interactive:  false,
			errContains:  "failed running terraform output", // source uses "output" for Show error
		},
		{
			name: "Destroy",
			invoke: func(cli *Cli, ctx context.Context) (string, error) {
				return cli.Destroy(ctx, "/module")
			},
			expectedArgs: []string{"-chdir=/module", "destroy"},
			interactive:  true,
			errContains:  "failed running terraform destroy",
		},
	}
}

func Test_Commands_Success(t *testing.T) {
	for _, tc := range getCommandTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			var capturedArgs exec.RunArgs

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return args.Cmd == "terraform"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				return exec.NewRunResult(0, "command output", ""), nil
			})

			cli := NewCli(mockContext.CommandRunner)
			result, err := tc.invoke(cli, *mockContext.Context)

			require.NoError(t, err)
			require.Equal(t, "command output", result)
			require.Equal(t, "terraform", capturedArgs.Cmd)
			require.Equal(t, tc.expectedArgs, capturedArgs.Args)
			require.Equal(t, tc.interactive, capturedArgs.Interactive)
		})
	}
}

func Test_Commands_Error(t *testing.T) {
	for _, tc := range getCommandTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return args.Cmd == "terraform"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.NewRunResult(1, "", "stderr details"), fmt.Errorf("exit code: 1")
			})

			cli := NewCli(mockContext.CommandRunner)
			result, err := tc.invoke(cli, *mockContext.Context)

			require.Error(t, err)
			require.Empty(t, result)
			require.Contains(t, err.Error(), tc.errContains)
			require.Contains(t, err.Error(), "stderr details")
			require.ErrorContains(t, err, "exit code: 1")
		})
	}
}

func Test_Commands_AdditionalArgs(t *testing.T) {
	t.Run("Init_WithBackendConfig", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		var capturedArgs exec.RunArgs

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.Init(*mockContext.Context, "/module", "-backend=false", "-input=false")

		require.NoError(t, err)
		require.Equal(t, []string{
			"-chdir=/module", "init", "-upgrade", "-backend=false", "-input=false",
		}, capturedArgs.Args)
	})

	t.Run("Plan_WithVar", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		var capturedArgs exec.RunArgs

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.Plan(*mockContext.Context, "/module", "/plan.out", "-var=key=value")

		require.NoError(t, err)
		require.Equal(t, []string{
			"-chdir=/module", "plan", "-out=/plan.out", "-lock=false", "-var=key=value",
		}, capturedArgs.Args)
	})

	t.Run("Apply_WithAutoApprove", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		var capturedArgs exec.RunArgs

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.Apply(*mockContext.Context, "/module", "-auto-approve")

		require.NoError(t, err)
		require.Equal(t, []string{
			"-chdir=/module", "apply", "-lock=false", "-auto-approve",
		}, capturedArgs.Args)
	})

	t.Run("Output_WithNoColor", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		var capturedArgs exec.RunArgs

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.Output(*mockContext.Context, "/module", "-no-color")

		require.NoError(t, err)
		require.Equal(t, []string{
			"-chdir=/module", "output", "-json", "-no-color",
		}, capturedArgs.Args)
	})

	t.Run("Show_WithPlanFile", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		var capturedArgs exec.RunArgs

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.Show(*mockContext.Context, "/module", "plan.out")

		require.NoError(t, err)
		require.Equal(t, []string{
			"-chdir=/module", "show", "-json", "plan.out",
		}, capturedArgs.Args)
	})

	t.Run("Destroy_WithAutoApprove", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		var capturedArgs exec.RunArgs

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.Destroy(*mockContext.Context, "/module", "-auto-approve")

		require.NoError(t, err)
		require.Equal(t, []string{
			"-chdir=/module", "destroy", "-auto-approve",
		}, capturedArgs.Args)
	})
}

func Test_Commands_EnvPropagation(t *testing.T) {
	envVars := []string{"TF_DATA_DIR=/tmp/tf", "ARM_CLIENT_ID=abc123"}

	// Test env propagation for a non-interactive command (runCommand path)
	t.Run("NonInteractive_Validate", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		var capturedArgs exec.RunArgs

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

		cli := NewCli(mockContext.CommandRunner)
		cli.SetEnv(envVars)
		_, err := cli.Validate(*mockContext.Context, "/module")

		require.NoError(t, err)
		require.Equal(t, envVars, capturedArgs.Env)
		require.False(t, capturedArgs.Interactive)
	})

	// Test env propagation for an interactive command (runInteractive path)
	t.Run("Interactive_Apply", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		var capturedArgs exec.RunArgs

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

		cli := NewCli(mockContext.CommandRunner)
		cli.SetEnv(envVars)
		_, err := cli.Apply(*mockContext.Context, "/module")

		require.NoError(t, err)
		require.Equal(t, envVars, capturedArgs.Env)
		require.True(t, capturedArgs.Interactive)
	})

	// Test that nil env is propagated when SetEnv is not called
	t.Run("NilEnvByDefault", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		var capturedArgs exec.RunArgs

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "terraform"
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.Output(*mockContext.Context, "/module")

		require.NoError(t, err)
		require.Nil(t, capturedArgs.Env)
	})
}

func Test_SetEnv(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	require.Nil(t, cli.env)

	envVars := []string{"KEY1=val1", "KEY2=val2"}
	cli.SetEnv(envVars)
	require.Equal(t, envVars, cli.env)

	// Overwrite with new env
	newEnvVars := []string{"KEY3=val3"}
	cli.SetEnv(newEnvVars)
	require.Equal(t, newEnvVars, cli.env)
}

func Test_NewCli(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	require.NotNil(t, cli)
	require.NotNil(t, cli.commandRunner)
	require.Nil(t, cli.env)
}

func Test_ImplementsExternalTool(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	// Verify the Cli type satisfies the tools.ExternalTool interface
	var _ tools.ExternalTool = cli
}
