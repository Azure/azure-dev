// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package cli_test contains end-to-end tests for azd.
package cli_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/sethvargo/go-retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

const (
	testSubscriptionId = "2cd617ea-1866-46b1-90e3-fffb087ebf9b"
	defaultLocation    = "eastus2"
)

func Test_CLI_Init_AsksForSubscriptionIdAndCreatesEnvAndProjectFile(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	_, err := cli.RunCommandWithStdIn(
		ctx,
		fmt.Sprintf("Empty Template\nTESTENV\nOther (enter manually)\n%s\n\n", testSubscriptionId),
		"init",
	)
	require.NoError(t, err)

	file, err := os.ReadFile(getTestEnvPath(dir, "TESTENV"))

	require.NoError(t, err)

	require.Regexp(t, regexp.MustCompile(fmt.Sprintf(`AZURE_SUBSCRIPTION_ID="%s"`, testSubscriptionId)+"\n"), string(file))
	require.Regexp(t, regexp.MustCompile(`AZURE_ENV_NAME="TESTENV"`+"\n"), string(file))

	proj, err := project.LoadProjectConfig(filepath.Join(dir, azdcontext.ProjectFileName))
	require.NoError(t, err)

	require.Equal(t, filepath.Base(dir), proj.Name)
}

func Test_CLI_Init_CanUseTemplate(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	_, err := cli.RunCommandWithStdIn(
		ctx,
		"TESTENV\n\nOther (enter manually)\nMY_SUB_ID\n",
		"init",
		"--template",
		"cosmos-dotnet-core-todo-app",
	)
	require.NoError(t, err)

	// While `init` uses git behind the scenes to pull a template, we don't want to bring the history over or initialize a
	// git
	// repository.
	require.NoDirExists(t, filepath.Join(dir, ".git"))

	// Ensure the project was initialized from the template by checking that a file from the template is present.
	require.FileExists(t, filepath.Join(dir, "README.md"))
}

func Test_CLI_InfraCreateAndDelete(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForTests(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "infra", "create")
	require.NoError(t, err)

	envPath := filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName)
	env, err := environment.FromRoot(envPath)
	require.NoError(t, err)

	// AZURE_STORAGE_ACCOUNT_NAME is an output of the template, make sure it was added to the .env file.
	// the name should start with 'st'
	accountName, ok := env.Values["AZURE_STORAGE_ACCOUNT_NAME"]
	require.True(t, ok)
	require.Regexp(t, `st\S*`, accountName)

	// GetResourceGroupsForEnvironment requires a credential since it is using the SDK now
	cred, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		t.Fatal("could not create credential")
	}

	azCli := azcli.NewAzCli(cred, azcli.NewAzCliArgs{})

	// Verify that resource groups are created with tag
	resourceManager := infra.NewAzureResourceManager(azCli)
	rgs, err := resourceManager.GetResourceGroupsForEnvironment(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, rgs)

	// Using `down` here to test the down alias to infra delete
	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}

func Test_CLI_InfraCreateAndDeleteUpperCase(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := "UpperCase" + randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForTests(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "infra", "create")
	require.NoError(t, err)

	envPath := filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName)
	env, err := environment.FromRoot(envPath)
	require.NoError(t, err)

	// AZURE_STORAGE_ACCOUNT_NAME is an output of the template, make sure it was added to the .env file.
	// the name should start with 'st'
	accountName, ok := env.Values["AZURE_STORAGE_ACCOUNT_NAME"]
	require.True(t, ok)
	require.Regexp(t, `st\S*`, accountName)

	// GetResourceGroupsForEnvironment requires a credential since it is using the SDK now
	cred, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		t.Fatal("could not create credential")
	}

	azCli := azcli.NewAzCli(cred, azcli.NewAzCliArgs{})

	// Verify that resource groups are created with tag
	resourceManager := infra.NewAzureResourceManager(azCli)
	rgs, err := resourceManager.GetResourceGroupsForEnvironment(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, rgs)

	// Using `down` here to test the down alias to infra delete
	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}

// test for azd deploy, azd deploy --service
func Test_CLI_DeployInvalidName(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForTests(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "deploy", "--service", "badServiceName")
	require.Error(t, err)
}

func Test_CLI_RestoreCommand(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "restoreapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForTests(envName), "restore")
	require.NoError(t, err)

	require.DirExists(t, path.Join(dir, "nodeapp", "node_modules", "chalk"), "nodeapp not restored")
	require.DirExists(t, path.Join(dir, "containerapp", "node_modules", "chalk"), "containerapp not restored")
	require.DirExists(t, path.Join(dir, "pyapp", "pyapp_env"), "pyapp not restored")
	require.DirExists(t, path.Join(dir, "csharpapp", "obj"), "csharpapp not restored")
	require.DirExists(t, path.Join(dir, "funcapp", "funcapp_env"), "funcapp not restored")
}

func Test_CLI_ProjectIsNeeded(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	tests := []struct {
		command string
		args    []string
	}{
		{command: "deploy"},
		{command: "down"},
		{command: "env get-values"},
		{command: "env list"},
		{command: "env new", args: []string{"testEnvironmentName"}},
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
		args := []string{"--cwd", dir}
		args = append(args, strings.Split(test.command, " ")...)
		if len(test.args) > 0 {
			args = append(args, test.args...)
		}

		t.Run(test.command, func(t *testing.T) {
			result, err := cli.RunCommand(ctx, args...)
			assert.Error(t, err)
			assert.Contains(t, result.Stderr, azdcontext.ErrNoProject.Error())
		})
	}
}

func Test_CLI_NoDebugSpewWhenHelpPassedWithoutDebug(t *testing.T) {
	stdErrBuf := bytes.Buffer{}

	cmd := osexec.Command(azdcli.GetAzdLocation(), "--help")
	cmd.Stderr = &stdErrBuf

	// Run the command and wait for it to complete, we don't expect any errors.
	err := cmd.Start()
	assert.NoError(t, err)

	err = cmd.Wait()
	assert.NoError(t, err)

	// Ensure no output was written to stderr
	assert.Equal(t, "", stdErrBuf.String(), "no output should be written to stderr when --help is passed")
}

//go:embed testdata/samples/*
var samples embed.FS

// copySample copies the given sample to targetRoot.
func copySample(targetRoot string, sampleName string) error {
	sampleRoot := path.Join("testdata", "samples", sampleName)

	return fs.WalkDir(samples, sampleRoot, func(name string, d fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		targetPath := filepath.Join(targetRoot, name[len(sampleRoot):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(samples, name)
		if err != nil {
			return fmt.Errorf("reading sample file: %w", err)
		}
		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}

func randomEnvName() string {
	bytes := make([]byte, 4)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(fmt.Errorf("could not read random bytes: %w", err))
	}

	// Adding first letter initial of the OS for CI identification
	osName := os.Getenv("AZURE_DEV_CI_OS")
	if osName == "" {
		osName = runtime.GOOS
	}
	osInitial := osName[:1]

	return ("azdtest-" + osInitial + hex.EncodeToString(bytes))[0:15]
}

// stdinForTests is just enough stdin to bypass all the prompts or choose defaults.
func stdinForTests(envName string) string {
	return fmt.Sprintf("%s\n", envName) + // "enter deployment name"
		"\n" + // "choose location" (we're choosing the default)
		"\n" // "choose subscription" (we're choosing the default)
}

func getTestEnvPath(dir string, envName string) string {
	return filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env")
}

// newTestContext returns a new empty context, suitable for use in tests. If a
// the provided `testing.T` has a deadline applied, the returned context
// respects the deadline.
func newTestContext(t *testing.T) (context.Context, context.CancelFunc) {
	ctx := context.Background()

	if deadline, ok := t.Deadline(); ok {
		return context.WithDeadline(ctx, deadline)
	}

	return context.WithCancel(ctx)
}

func Test_CLI_InfraCreateAndDeleteResourceTerraform(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "resourcegroupterraform")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForTests(envName), "init")
	require.NoError(t, err)

	t.Logf("Starting infra create\n")
	_, err = cli.RunCommand(ctx, "infra", "create", "--cwd", dir)
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "get-values", "-o", "json", "--cwd", dir)
	require.NoError(t, err)

	t.Logf("Starting infra delete\n")
	_, err = cli.RunCommand(ctx, "infra", "delete", "--cwd", dir, "--force", "--purge")
	require.NoError(t, err)

	t.Logf("Done\n")
}

func Test_CLI_InfraCreateAndDeleteResourceTerraformRemote(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	location := "eastus2"
	backendResourceGroupName := fmt.Sprintf("rs-%s", envName)
	backendStorageAccountName := strings.Replace(envName, "-", "", -1)
	backendContainerName := "tfstate"

	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), fmt.Sprintf("AZURE_LOCATION=%s", location))

	err := copySample(dir, "resourcegroupterraformremote")
	require.NoError(t, err, "failed expanding sample")

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
	storageAccountKey := strings.ReplaceAll(strings.ReplaceAll(cmdResult.Stdout, "\n", ""), "\r", "")

	// Create storage container
	runArgs = newRunArgs("az", "storage", "container", "create", "--name", backendContainerName,
		"--account-name", backendStorageAccountName, "--account-key", storageAccountKey)
	result, err := commandRunner.Run(ctx, runArgs)
	_ = result
	require.NoError(t, err)

	//Run azd init
	_, err = cli.RunCommandWithStdIn(ctx, stdinForTests(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "RS_STORAGE_ACCOUNT", backendStorageAccountName, "--cwd", dir)
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "RS_CONTAINER_NAME", backendContainerName, "--cwd", dir)
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "RS_RESOURCE_GROUP", backendResourceGroupName, "--cwd", dir)
	require.NoError(t, err)

	t.Logf("Starting infra create\n")
	_, err = cli.RunCommand(ctx, "infra", "create", "--cwd", dir)
	require.NoError(t, err)

	t.Logf("Starting infra delete\n")
	_, err = cli.RunCommand(ctx, "infra", "delete", "--cwd", dir, "--force", "--purge")
	require.NoError(t, err)

	t.Logf("Done\n")
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

// TempDirWithDiagnostics creates a temp directory with cleanup that also provides additional
// diagnostic logging and retries.
func tempDirWithDiagnostics(t *testing.T) string {
	temp := t.TempDir()

	if runtime.GOOS == "windows" {
		// Enable our additional custom remove logic for Windows where we see locked files.
		t.Cleanup(func() {
			err := removeAllWithDiagnostics(t, temp)
			if err != nil {
				logHandles(t, temp)
				t.Fatalf("TempDirWithDiagnostics: %s", err)
			}
		})
	}

	return temp
}

func logHandles(t *testing.T, path string) {
	handle, err := osexec.LookPath("handle")
	if err != nil && errors.Is(err, osexec.ErrNotFound) {
		t.Logf("handle.exe not present. Skipping handle detection. PATH: %s", os.Getenv("PATH"))
		return
	}

	if err != nil {
		t.Logf("failed to find handle.exe: %s", err)
		return
	}

	args := exec.NewRunArgs(handle, path, "-nobanner")
	cmd := exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
	rr, err := cmd.Run(context.Background(), args)
	if err != nil {
		t.Logf("handle.exe failed. stdout: %s, stderr: %s\n", rr.Stdout, rr.Stderr)
		return
	}

	t.Logf("handle.exe output:\n%s\n", rr.Stdout)

	// Ensure telemetry is initialized since we're running in a CI environment
	_ = telemetry.GetTelemetrySystem()

	// Log this to telemetry for ease of correlation
	tracer := telemetry.GetTracer()
	_, span := tracer.Start(context.Background(), "test.file_cleanup_failure")
	span.SetAttributes(attribute.String("handle.stdout", rr.Stdout))
	span.SetAttributes(attribute.String("ci.build.number", os.Getenv("BUILD_BUILDNUMBER")))
	span.End()
}

func removeAllWithDiagnostics(t *testing.T, path string) error {
	retryCount := 0
	loggedOnce := false
	return retry.Do(
		context.Background(),
		retry.WithMaxRetries(10, retry.NewConstant(1*time.Second)),
		func(_ context.Context) error {
			removeErr := os.RemoveAll(path)
			if removeErr == nil {
				return nil
			}
			t.Logf("failed to clean up %s with error: %v", path, removeErr)

			if retryCount >= 2 && !loggedOnce {
				// Only log once after 2 seconds - logHandles is pretty expensive and slow
				logHandles(t, path)
				loggedOnce = true
			}

			retryCount++
			return retry.RetryableError(removeErr)
		},
	)
}
