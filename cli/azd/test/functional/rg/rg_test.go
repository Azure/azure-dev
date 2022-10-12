// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package rg_test

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/functional"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_CLI_InfraCreateAndDelete(t *testing.T) {
	for _, tt := range []struct {
		name   string
		prefix string
	}{
		{"default", ""},
		{"withUpperCase", "UpperCase"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZURE_LOCATION", "eastus2")

			ctx, cancel := functional.NewTestContext(t)
			defer cancel()

			dir := functional.TempDirWithDiagnostics(t)
			ostest.Chdir(t, dir)

			err := functional.CopySample(dir, "storage")
			require.NoError(t, err, "failed expanding sample")

			envName, initResp := functional.NewRandomNameEnvAndInitResponseWithPrefix(t, tt.prefix)

			_, _, err = functional.RunCliCommandWithStdIn(t, ctx, initResp, "init")
			require.NoError(t, err)

			_, _, err = functional.RunCliCommand(t, ctx, "infra", "create")
			require.NoError(t, err)

			envFilePath := filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env")
			env, err := environment.FromFile(envFilePath)
			require.NoError(t, err)

			// AZURE_STORAGE_ACCOUNT_NAME is an output of the template, make sure it was added to the .env file.
			// the name should start with 'st'
			accountName, ok := env.Values["AZURE_STORAGE_ACCOUNT_NAME"]
			require.True(t, ok)
			require.Regexp(t, `st\S*`, accountName)

			// TODO(ellismg): We had this code before, but it was broken during a refactoring since we no longer
			// inject root command options into test contexts.  Figure out how we want to validate this (I assume
			// we should just use the SDK directly?. Or not test GetResourceGroupsForEnvironment in this way.
			//
			// // NewAzureResourceManager needs a command runner right now (since it can call the AZ CLI)
			// ctx = exec.WithCommandRunner(ctx, exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr))
			//
			// Verify that resource groups are created with tag
			// resourceManager := infra.NewAzureResourceManager(ctx)
			// rgs, err := resourceManager.GetResourceGroupsForEnvironment(ctx, env)
			// require.NoError(t, err)
			// require.NotNil(t, rgs)

			// Using `down` here to test the down alias to infra delete
			_, _, err = functional.RunCliCommand(t, ctx, "down", "--force", "--purge")
			require.NoError(t, err)
		})
	}
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
