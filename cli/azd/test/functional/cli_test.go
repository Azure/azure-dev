// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package cli_test contains end-to-end tests for azd.
package cli_test

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/devcenter"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/benbjohnson/clock"
	"github.com/joho/godotenv"
	"github.com/sethvargo/go-retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

// The current running configuration for the test suite.
var cfg = cliConfig{}

func init() {
	cfg.init()
}

// Configuration for the test suite.
type cliConfig struct {
	// If true, the test is running in CI.
	// This can be used to ensure tests that are skipped locally (due to complex setup), always strictly run in CI.
	CI bool

	// The client ID to use for live Azure tests.
	ClientID string
	// The tenant ID to use for live Azure tests.
	TenantID string
	// The Azure subscription ID to use for live Azure tests.
	// In non-CI environments with no additional environment variables set,
	// the azd user config 'defaults.subscription' value is used.
	SubscriptionID string
	// The Azure location to use for live Azure tests.
	// In non-CI environments with no additional environment variables set,
	// the azd user config 'defaults.location' value is used.
	Location string
}

func (c *cliConfig) init() {
	c.CI = os.Getenv("CI") != ""
	c.ClientID = os.Getenv("AZD_TEST_CLIENT_ID")
	c.TenantID = os.Getenv("AZD_TEST_TENANT_ID")
	c.SubscriptionID = os.Getenv("AZD_TEST_AZURE_SUBSCRIPTION_ID")
	c.Location = os.Getenv("AZD_TEST_AZURE_LOCATION")

	if !c.CI && (c.SubscriptionID == "" || c.Location == "") {
		userConfig := config.NewUserConfigManager(config.NewFileConfigManager(config.NewManager()))
		cfg, err := userConfig.Load()
		if err == nil {
			if subId, ok := cfg.GetString("defaults.subscription"); ok && c.SubscriptionID == "" {
				c.SubscriptionID = subId
			}

			if loc, ok := cfg.GetString("defaults.location"); ok && c.Location == "" {
				c.Location = loc
			}
		}

		if err != nil {
			log.Printf("could not load user config to provide default test values: %v", err)
		}
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

func Test_CLI_DevCenter_Init_Up_Down(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Skip("getting UnknownEnvironmentOperationError during deployment")
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	session := recording.Start(t)
	envName := randomOrStoredEnvName(session)

	// This test leverages a real dev center configuration with the following values:
	devCenterName := "dc-azd-o2pst6gaydv5o"
	catalogName := "wbreza"
	projectName := "Project-1"
	environmentDefinitionName := "HelloWorld"
	environmentTypeName := "Dev"

	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, fmt.Sprintf("%s=devcenter", environment.PlatformTypeEnvVarName))
	cli.Env = append(cli.Env, fmt.Sprintf("%s=%s", devcenter.DevCenterNameEnvName, devCenterName))
	cli.Env = append(cli.Env, fmt.Sprintf("%s=%s", devcenter.DevCenterCatalogEnvName, catalogName))

	initStdIn := strings.Join(
		[]string{
			"Select a template",
			environmentDefinitionName,
			envName,
			projectName,
		},
		"\n",
	)

	defer cleanupDeployments(ctx, t, cli, session, envName)

	// azd init
	_, err := cli.RunCommandWithStdIn(ctx, initStdIn, "init")
	require.NoError(t, err)

	// evaluate the project and environment configuration
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)
	projectConfig, err := project.Load(ctx, azdCtx.ProjectPath())
	require.NoError(t, err)

	require.Equal(t, devCenterName, projectConfig.Platform.Config["name"])
	require.Equal(t, catalogName, projectConfig.Platform.Config["catalog"])
	require.Equal(t, environmentDefinitionName, projectConfig.Platform.Config["environmentDefinition"])

	env, err := envFromAzdRoot(ctx, dir, envName)
	require.NoError(t, err)

	require.Equal(t, envName, env.Name())
	actualProjectName, _ := env.Config.Get(devcenter.DevCenterProjectPath)
	repoUrl, _ := env.Config.Get("provision.parameters.repoUrl")
	require.Equal(t, projectName, actualProjectName)
	require.Equal(t, "https://github.com/wbreza/azd-hello-world", repoUrl)

	// azd up
	upStdIn := strings.Join([]string{environmentTypeName}, "\n")
	_, err = cli.RunCommandWithStdIn(ctx, upStdIn, "up")
	require.NoError(t, err)

	// re-evaluate the environment configuration
	env, err = envFromAzdRoot(ctx, dir, envName)
	require.NoError(t, err)

	actualEnvTypeName, _ := env.Config.Get(devcenter.DevCenterEnvTypePath)
	require.Equal(t, environmentTypeName, actualEnvTypeName)

	// azd down
	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}

func Test_CLI_InfraCreateAndDelete(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	session := recording.Start(t)

	envName := randomOrStoredEnvName(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)

	env, err := envFromAzdRoot(ctx, dir, envName)
	require.NoError(t, err)

	// AZURE_STORAGE_ACCOUNT_NAME is an output of the template, make sure it was added to the .env file.
	// the name should start with 'st'
	accountName, ok := env.Dotenv()["AZURE_STORAGE_ACCOUNT_NAME"]
	require.True(t, ok)
	require.Regexp(t, `st\S*`, accountName)

	assertEnvValuesStored(t, env)

	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = env.GetSubscriptionId()
	}

	// GetResourceGroupsForEnvironment requires a credential since it is using the SDK now
	cred := azdcli.NewTestCredential(cli)

	var client *http.Client
	if session != nil {
		client = session.ProxyClient
	} else {
		client = http.DefaultClient
	}

	armClientOptions := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: client,
			Cloud:     cloud.AzurePublic().Configuration,
		},
	}

	credentialProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return cred, nil
		},
	)

	resourceService := azapi.NewResourceService(credentialProvider, armClientOptions)

	deploymentOperations := azapi.NewStandardDeployments(
		credentialProvider,
		armClientOptions,
		resourceService,
		cloud.AzurePublic(),
		clock.NewMock(),
	)

	// Verify that resource groups are created with tag
	resourceManager := infra.NewAzureResourceManager(resourceService, deploymentOperations)
	rgs, err := resourceManager.GetResourceGroupsForEnvironment(ctx, env.GetSubscriptionId(), env.Name())
	require.NoError(t, err)
	require.NotNil(t, rgs)

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}

func Test_CLI_ProvisionState(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	session := recording.Start(t)

	envName := randomOrStoredEnvName(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	expectedOutputContains := "There are no changes to provision for your application."

	// Provision preview should show creation of storage account
	preview, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision", "--preview")
	require.NoError(t, err)
	require.Contains(t, preview.Stdout, "Create : Storage account")

	// First provision creates all resources
	initial, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)
	require.NotContains(t, initial.Stdout, expectedOutputContains)

	// Second preview shows no changes required for storage account
	secondPreview, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision", "--preview")
	require.NoError(t, err)
	require.NotContains(t, secondPreview.Stdout, "Skip : Storage account")

	// Second provision should use cache
	secondProvisionOutput, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)
	require.Contains(t, secondProvisionOutput.Stdout, expectedOutputContains)

	// Third deploy setting a different param
	cli.Env = append(cli.Env, "INT_TAG_VALUE=1989")
	thirdProvisionOutput, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)
	require.NotContains(t, thirdProvisionOutput.Stdout, expectedOutputContains)

	// last provision should use cache
	lastProvisionOutput, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)
	require.Contains(t, lastProvisionOutput.Stdout, expectedOutputContains)

	// use flag to force provision
	flagProvisionOutput, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision", "--no-state")
	require.NoError(t, err)
	require.NotContains(t, flagProvisionOutput.Stdout, expectedOutputContains)

	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = env[environment.SubscriptionIdEnvVarName]
	}

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}

func Test_CLI_ProvisionStateWithDown(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	session := recording.Start(t)

	envName := randomOrStoredEnvName(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	expectedOutputContains := "There are no changes to provision for your application."

	initial, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)
	require.NotContains(t, initial.Stdout, expectedOutputContains)

	// Second provision should use cache
	secondProvisionOutput, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)
	require.Contains(t, secondProvisionOutput.Stdout, expectedOutputContains)

	// down to delete resources
	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)

	// use flag to force provision
	reProvisionAfterDown, err := cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)
	require.NotContains(t, reProvisionAfterDown.Stdout, expectedOutputContains)

	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = env[environment.SubscriptionIdEnvVarName]
	}

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

	session := recording.Start(t)

	envName := "UpperCase" + randomEnvName()
	if session != nil {
		if session.Playback {
			if _, ok := session.Variables[recording.EnvNameKey]; ok {
				envName = session.Variables[recording.EnvNameKey]
			}
		} else {
			session.Variables[recording.EnvNameKey] = envName
		}
	}

	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	err := copySample(dir, "storage")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// test 'infra create' alias
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision", "--output", "json")
	require.NoError(t, err)

	env, err := envFromAzdRoot(ctx, dir, envName)
	require.NoError(t, err)

	// AZURE_STORAGE_ACCOUNT_NAME is an output of the template, make sure it was added to the .env file.
	// the name should start with 'st'
	accountName, ok := env.Dotenv()["AZURE_STORAGE_ACCOUNT_NAME"]
	require.True(t, ok)
	require.Regexp(t, `st\S*`, accountName)

	assertEnvValuesStored(t, env)

	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = env.GetSubscriptionId()
	}

	// GetResourceGroupsForEnvironment requires a credential since it is using the SDK now
	var client *http.Client
	if session != nil {
		client = session.ProxyClient
	} else {
		client = http.DefaultClient
	}

	cred := azdcli.NewTestCredential(cli)

	armClientOptions := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: client,
			Cloud:     cloud.AzurePublic().Configuration,
		},
	}

	credentialProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return cred, nil
		},
	)

	resourceService := azapi.NewResourceService(credentialProvider, armClientOptions)

	deploymentOperations := azapi.NewStandardDeployments(
		credentialProvider,
		armClientOptions,
		resourceService,
		cloud.AzurePublic(),
		clock.NewMock(),
	)

	// Verify that resource groups are created with tag
	resourceManager := infra.NewAzureResourceManager(resourceService, deploymentOperations)
	rgs, err := resourceManager.GetResourceGroupsForEnvironment(ctx, env.GetSubscriptionId(), env.Name())
	require.NoError(t, err)
	require.NotNil(t, rgs)

	// test 'infra delete' alias
	_, err = cli.RunCommand(ctx, "down", "--force", "--purge", "--output", "json")
	require.NoError(t, err)
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
		{command: "provision"},
		{command: "deploy"},
		{command: "up"},
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
		{command: "restore"},
	}

	for _, tt := range tests {
		test := tt
		args := []string{"--cwd", dir}
		args = append(args, strings.Split(test.command, " ")...)
		if len(test.args) > 0 {
			args = append(args, test.args...)
		}

		t.Run(test.command, func(t *testing.T) {
			result, err := cli.RunCommand(ctx, args...)
			assert.Error(t, err)
			assert.Contains(t, result.Stdout, azdcontext.ErrNoProject.Error())
		})
	}
}

// Verifies commands that requires `azd provision` to be successfully run beforehand correctly errors out.
func Test_CLI_ProvisionIsNeeded(t *testing.T) {
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

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	tests := []struct {
		command       string
		args          []string
		errorToStdOut bool
	}{
		{command: "deploy"},
		{command: "monitor"},
	}

	for _, tt := range tests {
		test := tt
		args := []string{"--cwd", dir}
		args = append(args, strings.Split(test.command, " ")...)
		if len(test.args) > 0 {
			args = append(args, test.args...)
		}

		t.Run(test.command, func(t *testing.T) {
			result, err := cli.RunCommand(ctx, args...)
			assert.Error(t, err)
			assert.Contains(t, result.Stdout, "azd provision")
		})
	}
}

// Verifies support for bicepparam
func Test_CLI_InfraBicepParam(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	session := recording.Start(t)

	envName := randomEnvName()
	if session != nil {
		if session.Playback {
			if _, ok := session.Variables[recording.EnvNameKey]; ok {
				envName = session.Variables[recording.EnvNameKey]
			}
		} else {
			session.Variables[recording.EnvNameKey] = envName
		}
	}

	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	err := copySample(dir, "storage-bicepparam")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// test 'infra create' alias
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision", "--output", "json")
	require.NoError(t, err)

	env, err := envFromAzdRoot(ctx, dir, envName)
	require.NoError(t, err)

	// WEBSITE_URL is an output of the template, make sure it was added to the .env file.
	// the name should start with 'st'
	accountName, ok := env.Dotenv()["AZURE_STORAGE_ACCOUNT_NAME"]
	require.True(t, ok)
	require.Regexp(t, `st\S*`, accountName)

	assertEnvValuesStored(t, env)

	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = env.GetSubscriptionId()
	}

	// Delete
	_, err = cli.RunCommand(ctx, "down", "--force", "--purge", "--output", "json")
	require.NoError(t, err)
}

// Test_CLI_NoDebugSpewWhenHelpPassedWithoutDebug ensures that no debug spew is written to stderr when --help is passed
func Test_CLI_NoDebugSpewWhenHelpPassedWithoutDebug(t *testing.T) {
	cli := azdcli.NewCLI(t)
	// Update checks are one of the things that can write to stderr. Disable it since it's not relevant to this test.
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZD_SKIP_UPDATE_CHECK=true")
	ctx := context.Background()
	result, err := cli.RunCommand(ctx, "--help")
	require.NoError(t, err)

	// Ensure no output was written to stderr
	assert.Equal(t, "", result.Stderr, "no output should be written to stderr when --help is passed")
}

//go:embed all:testdata/samples/*
var samples embed.FS

func samplePath(paths ...string) string {
	elem := append([]string{"testdata", "samples"}, paths...)
	return path.Join(elem...)
}

// copySample copies the given sample to targetRoot.
func copySample(targetRoot string, sampleName string) error {
	sampleRoot := samplePath(sampleName)

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

func randomOrStoredEnvName(session *recording.Session) string {
	if session != nil && session.Playback {
		if _, ok := session.Variables[recording.EnvNameKey]; ok {
			return session.Variables[recording.EnvNameKey]
		}
	}

	randName := randomEnvName()
	if session != nil {
		session.Variables[recording.EnvNameKey] = randName
	}

	return randName
}

func cfgOrStoredSubscription(session *recording.Session) string {
	if session != nil && session.Playback {
		if _, ok := session.Variables[recording.SubscriptionIdKey]; ok {
			return session.Variables[recording.SubscriptionIdKey]
		}
	}

	subID := cfg.SubscriptionID
	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = subID
	}

	return subID
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

// stdinForInit builds the standard input string that will configure a given environment name
// when `init` is run
func stdinForInit(envName string) string {
	return fmt.Sprintf("%s\n", envName)
}

// stdinForProvision is just enough stdin to accept the defaults for the two prompts
// from `provision` (for a subscription and location)
func stdinForProvision() string {
	return "\n" + // "choose subscription" (we're choosing the default)
		"\n" // "choose location" (we're choosing the default)
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

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	// turn alpha feature on
	_, err = cli.RunCommand(ctx, "config", "set", "alpha.terraform", "on")
	require.NoError(t, err)

	t.Logf("Starting provision\n")
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision", "--cwd", dir)
	require.NoError(t, err)

	env, err := envFromAzdRoot(ctx, dir, envName)
	require.NoError(t, err)
	assertEnvValuesStored(t, env)

	t.Logf("Starting down\n")
	_, err = cli.RunCommand(ctx, "down", "--cwd", dir, "--force", "--purge")
	require.NoError(t, err)

	t.Logf("Done\n")
}

func Test_CLI_InfraCreateAndDeleteResourceTerraformRemote(t *testing.T) {
	t.Skip("azure/azure-dev#4564")

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
	commandRunner := exec.NewCommandRunner(nil)
	runArgs := newRunArgs("az", "group", "create", "--name", backendResourceGroupName, "--location", location)

	_, err = commandRunner.Run(ctx, runArgs)
	require.NoError(t, err)

	defer func() {
		commandRunner := exec.NewCommandRunner(nil)
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
	runArgs.SensitiveData = append(runArgs.SensitiveData, storageAccountKey)
	result, err := commandRunner.Run(ctx, runArgs)
	_ = result
	require.NoError(t, err)

	//Run azd init
	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "RS_STORAGE_ACCOUNT", backendStorageAccountName, "--cwd", dir)
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "RS_CONTAINER_NAME", backendContainerName, "--cwd", dir)
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "RS_RESOURCE_GROUP", backendResourceGroupName, "--cwd", dir)
	require.NoError(t, err)

	// turn alpha feature on
	_, err = cli.RunCommand(ctx, "config", "set", "alpha.terraform", "on")
	require.NoError(t, err)

	t.Logf("Starting infra create\n")
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision", "--cwd", dir)
	require.NoError(t, err)

	t.Logf("Starting infra delete\n")
	_, err = cli.RunCommand(ctx, "down", "--cwd", dir, "--force", "--purge")
	require.NoError(t, err)

	t.Logf("Done\n")
}

func newRunArgs(cmd string, args ...string) exec.RunArgs {
	return exec.NewRunArgs(cmd, args...)
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
	cmd := exec.NewCommandRunner(nil)
	rr, err := cmd.Run(context.Background(), args)
	if err != nil {
		t.Logf("handle.exe failed. stdout: %s, stderr: %s\n", rr.Stdout, rr.Stderr)
		return
	}

	t.Logf("handle.exe output:\n%s\n", rr.Stdout)

	// Ensure telemetry is initialized since we're running in a CI environment
	_ = telemetry.GetTelemetrySystem()

	// Log this to telemetry for ease of correlation
	_, span := tracing.Start(context.Background(), "test.file_cleanup_failure")
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

// Assert that all supported types from the infrastructure provider is marshalled and stored correctly in the environment.
func assertEnvValuesStored(t *testing.T, env *environment.Environment) {
	expectedEnv, err := godotenv.Read(filepath.Join("testdata", "expected-output-types", "typed-values.env"))
	require.NoError(t, err)
	primitives := []string{"STRING", "BOOL", "INT"}

	for k, v := range expectedEnv {
		actual, has := env.Dotenv()[k]
		assert.True(t, has)

		if slices.Contains(primitives, k) {
			assert.Equal(t, v, actual)
		} else {
			assert.JSONEq(t, v, actual)
		}
	}
}

func envFromAzdRoot(ctx context.Context, azdRootDir string, envName string) (*environment.Environment, error) {
	azdCtx := azdcontext.NewAzdContextWithDirectory(azdRootDir)
	localDataStore := environment.NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))
	return localDataStore.Get(ctx, envName)
}
