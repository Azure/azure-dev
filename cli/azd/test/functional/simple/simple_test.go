// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package simple_test

import (
	"bytes"
	"flag"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/functional"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_CLI_FailsIfAzCliIsMissing(t *testing.T) {
	for _, command := range []string{"init", "login"} {
		t.Run(command, func(t *testing.T) {
			ctx, cancel := functional.NewTestContext(t)
			defer cancel()

			dir := functional.TempDirWithDiagnostics(t)
			ostest.Chdir(t, dir)
			ostest.Unsetenv(t, "PATH")

			_, stderr, err := functional.RunCliCommand(t, ctx, "", command)

			require.Error(t, err)
			require.Contains(t,
				stderr,
				"Azure CLI is not installed, please see https://aka.ms/azure-dev/azure-cli-install to install")
		})
	}
}

func Test_CLI_Version_PrintsVersion(t *testing.T) {
	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	stdout, stderr, err := functional.RunCliCommand(t, ctx, "version")

	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "azd version")
}

func Test_CLI_Init_AsksForSubscriptionIdAndCreatesEnvAndProjectFile(t *testing.T) {
	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)
	ostest.Setenv(t, "AZURE_LOCATION", "eastus2")

	stdIn := bytes.NewBufferString("Empty Template\nTESTENV\nOther (enter manually)\nMY_SUB_ID\n\n")

	_, _, err := functional.RunCliCommandWithStdIn(t, ctx, stdIn, "init")
	require.NoError(t, err)

	file, err := os.ReadFile(getTestEnvPath(dir, "TESTENV"))
	require.NoError(t, err)

	require.Regexp(t, regexp.MustCompile(`AZURE_SUBSCRIPTION_ID="MY_SUB_ID"`+"\n"), string(file))
	require.Regexp(t, regexp.MustCompile(`AZURE_ENV_NAME="TESTENV"`+"\n"), string(file))

	proj, err := project.LoadProjectConfig(filepath.Join(dir, azdcontext.ProjectFileName), environment.Ephemeral())
	require.NoError(t, err)

	require.Equal(t, filepath.Base(dir), proj.Name)
}

func Test_CLI_Init_CanUseTemplate(t *testing.T) {
	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)
	ostest.Setenv(t, "AZURE_LOCATION", "eastus2")

	stdin := bytes.NewBufferString("TESTENV\n\nOther (enter manually)\nMY_SUB_ID\n")

	_, _, err := functional.RunCliCommandWithStdIn(t, ctx, stdin, "init", "--template", "cosmos-dotnet-core-todo-app")
	require.NoError(t, err)

	// While `init` uses git behind the scenes to pull a template, we don't want to bring the history over or initialize a git
	// repository.
	require.NoDirExists(t, filepath.Join(dir, ".git"))

	// Ensure the project was initialized from the template by checking that a file from the template is present.
	require.FileExists(t, filepath.Join(dir, "README.md"))

}

// test for azd deploy, azd deploy --service
func Test_CLI_DeployInvalidName(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "eastus2")

	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)

	err := functional.CopySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, initResp := functional.NewRandomNameEnvAndInitResponse(t)

	_, _, err = functional.RunCliCommandWithStdIn(t, ctx, initResp, "init")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "deploy", "--service", "badServiceName")
	require.ErrorContains(t, err, "'badServiceName' doesn't exist")
}

func Test_CLI_ProjectIsNeeded(t *testing.T) {
	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)

	tests := []struct {
		command string
		args    []string
	}{
		{command: "deploy"},
		{command: "down"},
		{command: "env get-values"},
		{command: "env list"},
		{command: "env new"},
		{command: "env refresh"},
		{command: "env select", args: []string{"testEnvironmentName"}},
		{command: "env set", args: []string{"testKey", "testValue"}},
		{command: "infra create"},
		{command: "infra delete"},
		{command: "monitor"},
		{command: "pipeline config"},
		{command: "provision"},
		{command: "restore"},
	}

	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			args := strings.Split(test.command, " ")
			if test.args != nil {
				args = append(args, test.args...)
			}

			_, stderr, err := functional.RunCliCommand(t, ctx, args...)

			assert.Error(t, err)
			assert.Regexp(t, "no project exists; to create a new project, run `azd init`", stderr)
		})
	}
}

func Test_CLI_NoDebugSpewWhenHelpPassedWithoutDebug(t *testing.T) {
	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)

	_, stderr, err := functional.RunCliCommand(t, ctx, "--help")
	assert.NoError(t, err)

	assert.Empty(t, stderr, "no output should be written to stderr when --help is passed")
}

func getTestEnvPath(dir string, envName string) string {
	return filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env")
}

func TestMain(m *testing.M) {
	flag.Parse()
	shortFlag := flag.Lookup("test.short")
	if shortFlag != nil && shortFlag.Value.String() == "true" {
		log.Println("Skipping tests in short mode")
		os.Exit(0)
	}

	exitVal := m.Run()
	os.Exit(exitVal)
}
