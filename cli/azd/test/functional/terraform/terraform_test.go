// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terraform_test

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/functional"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_CLI_InfraCreateAndDeleteResourceTerraform(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "eastus2")

	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)

	err := functional.CopySample(dir, "resourcegroupterraform")
	require.NoError(t, err, "failed expanding sample")

	_, initResp := functional.NewRandomNameEnvAndInitResponse(t)

	_, _, err = functional.RunCliCommandWithStdIn(t, ctx, initResp, "init")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "infra", "create")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "env", "get-values", "-o", "json")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "infra", "delete", "--force", "--purge")
	require.NoError(t, err)

	fmt.Println()
}

func Test_CLI_InfraCreateAndDeleteResourceTerraformRemote(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "eastus2")

	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)

	err := functional.CopySample(dir, "resourcegroupterraformremote")
	require.NoError(t, err, "failed expanding sample")

	envName, initResp := functional.NewRandomNameEnvAndInitResponse(t)

	location := "eastus2"
	backendResourceGroupName := fmt.Sprintf("rs-%s", envName)
	backendStorageAccountName := strings.Replace(envName, "-", "", -1)
	backendContainerName := "tfstate"

	//Create remote state resources
	commandRunner := exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
	runArgs := newRunArgs("az", "group", "create", "--name", backendResourceGroupName, "--location", location)

	_, err = commandRunner.Run(ctx, runArgs)
	require.NoError(t, err)

	defer func() {
		commandRunner := exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
		runArgs := newRunArgs("az", "group", "delete", "--name", backendResourceGroupName, "--yes")
		_, err = commandRunner.Run(ctx, runArgs)
		require.NoError(t, err)
	}()

	//Create storage account
	runArgs = newRunArgs("az", "storage", "account", "create", "--resource-group", backendResourceGroupName,
		"--name", backendStorageAccountName, "--sku", "Standard_LRS", "--encryption-services", "blob")
	_, err = commandRunner.Run(ctx, runArgs)
	require.NoError(t, err)

	//Get Account Key
	runArgs = newRunArgs("az", "storage", "account", "keys", "list", "--resource-group",
		backendResourceGroupName, "--account-name", backendStorageAccountName, "--query", "[0].value",
		"-o", "tsv")
	cmdResult, err := commandRunner.Run(ctx, runArgs)
	require.NoError(t, err)
	storageAccountKey := cmdResult.Stdout

	// Create storage container
	runArgs = newRunArgs("az", "storage", "container", "create", "--name", backendContainerName,
		"--account-name", backendStorageAccountName, "--account-key", storageAccountKey)
	result, err := commandRunner.Run(ctx, runArgs)
	_ = result
	require.NoError(t, err)

	_, _, err = functional.RunCliCommandWithStdIn(t, ctx, initResp, "init")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "env", "set", "RS_STORAGE_ACCOUNT", backendStorageAccountName)
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "env", "set", "RS_CONTAINER_NAME", backendContainerName)
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "env", "set", "RS_RESOURCE_GROUP", backendResourceGroupName)
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "infra", "create")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "infra", "delete", "--force", "--purge")
	require.NoError(t, err)

	fmt.Println()
}

func newRunArgs(cmd string, args ...string) exec.RunArgs {
	runArgs := exec.NewRunArgs(cmd, args...)
	return runArgs.WithEnrichError(true)
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
