// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

func TestNewCli_DefaultsToNpm(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	cli := NewCli(runner)
	require.Equal(t, PackageManagerNpm, cli.PackageManager())
	require.Equal(t, "npm CLI", cli.Name())
	require.Equal(t, "https://nodejs.org/", cli.InstallUrl())
}

func TestNewCliWithPackageManager(t *testing.T) {
	tests := []struct {
		pm         PackageManagerKind
		name       string
		installUrl string
	}{
		{PackageManagerNpm, "npm CLI", "https://nodejs.org/"},
		{PackageManagerPnpm, "pnpm CLI", "https://pnpm.io/installation"},
		{PackageManagerYarn, "yarn CLI", "https://yarnpkg.com/getting-started/install"},
	}

	for _, tt := range tests {
		t.Run(string(tt.pm), func(t *testing.T) {
			runner := mockexec.NewMockCommandRunner()
			cli := NewCliWithPackageManager(runner, tt.pm)
			require.Equal(t, tt.pm, cli.PackageManager())
			require.Equal(t, tt.name, cli.Name())
			require.Equal(t, tt.installUrl, cli.InstallUrl())
		})
	}
}

func TestInstall_UsesCorrectBinaryAndFlags(t *testing.T) {
	tests := []struct {
		pm           PackageManagerKind
		expectedCmd  string
		expectedArgs []string
	}{
		{PackageManagerNpm, "npm", []string{"install", "--no-audit", "--no-fund", "--prefer-offline"}},
		{PackageManagerPnpm, "pnpm", []string{"install", "--prefer-offline"}},
		{PackageManagerYarn, "yarn", []string{"install"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.pm), func(t *testing.T) {
			runner := mockexec.NewMockCommandRunner()
			runner.When(func(args exec.RunArgs, command string) bool {
				if args.Cmd != tt.expectedCmd || len(args.Args) != len(tt.expectedArgs) {
					return false
				}
				for i, arg := range tt.expectedArgs {
					if args.Args[i] != arg {
						return false
					}
				}
				return true
			}).Respond(exec.RunResult{})

			cli := NewCliWithPackageManager(runner, tt.pm)
			err := cli.Install(context.Background(), "/project")
			require.NoError(t, err)
		})
	}
}

func TestRunScript_NpmUsesIfPresent(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		// npm puts --if-present after the script name: npm run build --if-present
		return args.Cmd == "npm" &&
			len(args.Args) == 3 &&
			args.Args[0] == "run" && args.Args[1] == "build" && args.Args[2] == "--if-present"
	}).Respond(exec.RunResult{})

	cli := NewCliWithPackageManager(runner, PackageManagerNpm)
	err := cli.RunScript(context.Background(), "/project", "build")
	require.NoError(t, err)
}

func TestRunScript_PnpmUsesIfPresentBeforeScript(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		// pnpm requires --if-present before the script name: pnpm run --if-present build
		return args.Cmd == "pnpm" &&
			len(args.Args) == 3 &&
			args.Args[0] == "run" && args.Args[1] == "--if-present" && args.Args[2] == "build"
	}).Respond(exec.RunResult{})

	cli := NewCliWithPackageManager(runner, PackageManagerPnpm)
	err := cli.RunScript(context.Background(), "/project", "build")
	require.NoError(t, err)
}

func TestRunScript_YarnChecksScriptExists(t *testing.T) {
	dir := t.TempDir()

	// package.json WITH build script
	err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"build":"tsc"}}`), 0600)
	require.NoError(t, err)

	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "yarn" &&
			len(args.Args) == 2 &&
			args.Args[0] == "run" && args.Args[1] == "build"
	}).Respond(exec.RunResult{})

	cli := NewCliWithPackageManager(runner, PackageManagerYarn)
	err = cli.RunScript(context.Background(), dir, "build")
	require.NoError(t, err)
}

func TestRunScript_YarnSkipsWhenScriptMissing(t *testing.T) {
	dir := t.TempDir()

	// package.json WITHOUT build script
	err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"test"}`), 0600)
	require.NoError(t, err)

	runner := mockexec.NewMockCommandRunner()
	// No mock for yarn run — it should NOT be called

	cli := NewCliWithPackageManager(runner, PackageManagerYarn)
	err = cli.RunScript(context.Background(), dir, "build")
	require.NoError(t, err) // should silently succeed
}

func TestPrune_PnpmUsesProdFlag(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "pnpm" &&
			strings.Contains(command, "prune") &&
			strings.Contains(command, "--prod")
	}).Respond(exec.RunResult{})

	cli := NewCliWithPackageManager(runner, PackageManagerPnpm)
	err := cli.Prune(context.Background(), "/project", true)
	require.NoError(t, err)
}

func TestPrune_NpmUsesProductionFlag(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "npm" &&
			strings.Contains(command, "prune") &&
			strings.Contains(command, "--production")
	}).Respond(exec.RunResult{})

	cli := NewCliWithPackageManager(runner, PackageManagerNpm)
	err := cli.Prune(context.Background(), "/project", true)
	require.NoError(t, err)
}

func TestPrune_YarnUsesInstallProduction(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		// Yarn prune is implemented as `yarn install --production`
		return args.Cmd == "yarn" &&
			strings.Contains(command, "install") &&
			strings.Contains(command, "--production")
	}).Respond(exec.RunResult{})

	cli := NewCliWithPackageManager(runner, PackageManagerYarn)
	err := cli.Prune(context.Background(), "/project", true)
	require.NoError(t, err)
}

func TestInstall_ReturnsError(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "pnpm"
	}).SetError(fmt.Errorf("command failed"))

	cli := NewCliWithPackageManager(runner, PackageManagerPnpm)
	err := cli.Install(context.Background(), "/project")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pnpm")
}

func TestRunScript_ReturnsError(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "npm"
	}).SetError(fmt.Errorf("script failed"))

	cli := NewCli(runner)
	err := cli.RunScript(context.Background(), "/project", "build")
	require.Error(t, err)
	require.Contains(t, err.Error(), "npm")
}

func TestPrune_WithoutProduction(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "npm" &&
			strings.Contains(command, "prune") &&
			!strings.Contains(command, "--production")
	}).Respond(exec.RunResult{})

	cli := NewCli(runner)
	err := cli.Prune(context.Background(), "/project", false)
	require.NoError(t, err)
}
