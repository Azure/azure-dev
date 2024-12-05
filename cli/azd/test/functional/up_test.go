// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/joho/godotenv"
	"github.com/sethvargo/go-retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const expectedTestAppResponse = "Hello, `azd`."

func Test_CLI_Up_Down_WebApp(t *testing.T) {
	t.Skip("azure-dev/834")
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

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)

	t.Logf("Running show\n")
	result, err := cli.RunCommand(ctx, "show", "-o", "json", "--cwd", dir)
	require.NoError(t, err)

	var showRes struct {
		Services map[string]struct {
			Project struct {
				Path     string `json:"path"`
				Language string `json:"language"`
			} `json:"project"`
			Target struct {
				ResourceIds []string `json:"resourceIds"`
			} `json:"target"`
		} `json:"services"`
	}
	err = json.Unmarshal([]byte(result.Stdout), &showRes)
	require.NoError(t, err)

	service, has := showRes.Services["web"]
	require.True(t, has)
	require.Equal(t, "dotnet", service.Project.Language)
	require.Equal(t, "webapp.csproj", filepath.Base(service.Project.Path))
	require.Equal(t, 1, len(service.Target.ResourceIds))

	_, err = cli.RunCommand(ctx, "deploy", "web")
	require.NoError(t, err)

	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	url, has := env["WEBSITE_URL"]
	require.True(t, has, "WEBSITE_URL should be in environment after infra create")

	err = probeServiceHealth(t, ctx, http.DefaultClient, retry.NewConstant(5*time.Second), url, expectedTestAppResponse)
	require.NoError(t, err)

	commandRunner := exec.NewCommandRunner(nil)
	runArgs := newRunArgs("dotnet", "user-secrets", "list", "--project", filepath.Join(dir, "/src/dotnet/webapp.csproj"))
	secrets, err := commandRunner.Run(ctx, runArgs)
	require.NoError(t, err)

	contain := strings.Contains(secrets.Stdout, fmt.Sprintf("WEBSITE_URL = %s", url))
	require.True(t, contain)

	// Ensure `env refresh` works by removing an output parameter from the .env file and ensure that `env refresh`
	// brings it back.
	delete(env, "WEBSITE_URL")
	err = godotenv.Write(env, filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	//clear dotnet secrets to test if dotnet secrets works when running env refresh
	runArgs = newRunArgs("dotnet", "user-secrets", "clear", "--project", filepath.Join(dir, "/src/dotnet/webapp.csproj"))
	secrets, err = commandRunner.Run(ctx, runArgs)
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "refresh", envName)
	require.NoError(t, err)

	env, err = godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	_, has = env["WEBSITE_URL"]
	require.True(t, has, "WEBSITE_URL should be in environment after refresh")

	runArgs = newRunArgs("dotnet", "user-secrets", "list", "--project", filepath.Join(dir, "/src/dotnet/webapp.csproj"))
	secrets, err = commandRunner.Run(ctx, runArgs)
	require.NoError(t, err)

	contain = strings.Contains(secrets.Stdout, fmt.Sprintf("WEBSITE_URL = %s", url))
	require.True(t, contain)

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)

	t.Logf("Running show (again)\n")
	result, err = cli.RunCommand(ctx, "show", "-o", "json", "--cwd", dir)
	require.NoError(t, err)

	err = json.Unmarshal([]byte(result.Stdout), &showRes)
	require.NoError(t, err)

	// Project information should be present, but since we have run infra delete, there shouldn't
	// be any resource ids.
	service, has = showRes.Services["web"]
	require.True(t, has)
	require.Equal(t, "dotnet", service.Project.Language)
	require.Equal(t, "webapp.csproj", filepath.Base(service.Project.Path))
	require.Nil(t, service.Target.ResourceIds)
}

func Test_CLI_Up_Down_FuncApp(t *testing.T) {
	t.Skip("azure-dev/834")
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

	err := copySample(dir, "funcapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	t.Logf("Starting infra create\n")
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision", "--cwd", dir)
	require.NoError(t, err)

	t.Logf("Starting deploy\n")
	_, err = cli.RunCommand(ctx, "deploy", "--cwd", dir)
	require.NoError(t, err)

	result, err := cli.RunCommand(ctx, "env", "get-values", "-o", "json", "--cwd", dir)
	require.NoError(t, err)

	t.Logf("env get-values command output: %s\n", result.Stdout)

	var envValues map[string]interface{}
	err = json.Unmarshal([]byte(result.Stdout), &envValues)
	require.NoError(t, err)

	url := fmt.Sprintf("%s/api/httptrigger", envValues["AZURE_FUNCTION_URI"])

	t.Logf("Issuing GET request to function\n")

	// We've seen some cases in CI where issuing a get right after a deploy ends up with us getting a 404, so retry the
	// request a
	// handful of times if it fails with a 404.
	err = retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(5*time.Second)), func(ctx context.Context) error {
		/* #nosec G107 - Potential HTTP request made with variable url false positive */
		res, err := http.Get(url)
		if err != nil {
			return retry.RetryableError(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return retry.RetryableError(
				fmt.Errorf("expected %d but got %d for request to %s", http.StatusOK, res.StatusCode, url),
			)
		}
		return nil
	})
	require.NoError(t, err)

	t.Logf("Starting infra delete\n")
	_, err = cli.RunCommand(ctx, "down", "--cwd", dir, "--force", "--purge")
	require.NoError(t, err)

	t.Logf("Done\n")
}

func Test_CLI_Up_Down_ContainerApp(t *testing.T) {
	t.Parallel()

	if ci_os := os.Getenv("AZURE_DEV_CI_OS"); ci_os != "" && ci_os != "lin" {
		t.Skip("Skipping due to docker limitations for non-linux systems on CI")
	}

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

	err := copySample(dir, "containerapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "deploy", "--cwd", filepath.Join(dir, "src", "dotnet"))
	require.NoError(t, err)

	// The sample hosts a small application that just responds with a 200 OK with a body of "Hello, `azd`."
	// (without the quotes). Validate that the application is working.
	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	url, has := env["WEBSITE_URL"]
	require.True(t, has, "WEBSITE_URL should be in environment after deploy")

	if session == nil {
		err = probeServiceHealth(
			t, ctx, http.DefaultClient, retry.NewConstant(5*time.Second), url, expectedTestAppResponse)
		require.NoError(t, err)
	} else {
		session.Variables[recording.SubscriptionIdKey] = env[environment.SubscriptionIdEnvVarName]

		err = probeServiceHealth(
			t, ctx, session.ProxyClient, retry.NewConstant(1*time.Millisecond), url, expectedTestAppResponse)
		require.NoError(t, err)
	}

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)

	// As part of deleting the infrastructure, outputs of the infrastructure such as "WEBSITE_URL" should
	// have been removed from the environment.
	env, err = godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	_, has = env["WEBSITE_URL"]
	require.False(t, has, "WEBSITE_URL should have been removed from the environment as part of infrastructure removal")
}

func Test_CLI_Up_Down_ContainerAppDotNetPublish(t *testing.T) {
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

	err := copySample(dir, "containerapp")
	require.NoError(t, err, "failed expanding sample")

	// Remove the Dockerfile so that we go down the `dotnet publish` path.
	err = os.Remove(filepath.Join(dir, "src", "dotnet", "Dockerfile"))
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "deploy", "--cwd", filepath.Join(dir, "src", "dotnet"))
	require.NoError(t, err)

	// The sample hosts a small application that just responds with a 200 OK with a body of "Hello, `azd`."
	// (without the quotes). Validate that the application is working.
	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	url, has := env["WEBSITE_URL"]
	require.True(t, has, "WEBSITE_URL should be in environment after deploy")

	if session == nil {
		err = probeServiceHealth(
			t, ctx, http.DefaultClient, retry.NewConstant(5*time.Second), url, expectedTestAppResponse)
		require.NoError(t, err)
	} else {
		session.Variables[recording.SubscriptionIdKey] = env[environment.SubscriptionIdEnvVarName]

		err = probeServiceHealth(
			t, ctx, session.ProxyClient, retry.NewConstant(1*time.Millisecond), url, expectedTestAppResponse)
		require.NoError(t, err)
	}

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)

	// As part of deleting the infrastructure, outputs of the infrastructure such as "WEBSITE_URL" should
	// have been removed from the environment.
	env, err = godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	_, has = env["WEBSITE_URL"]
	require.False(t, has, "WEBSITE_URL should have been removed from the environment as part of infrastructure removal")
}

func Test_CLI_Up_Down_ContainerApp_RemoteBuild(t *testing.T) {
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

	err := copySample(dir, "containerremotebuildapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "deploy", "--cwd", filepath.Join(dir, "src", "app"))
	require.NoError(t, err)

	// The sample hosts a small application that just responds with a 200 OK with a body of "Hello, `azd`."
	// (without the quotes). Validate that the application is working.
	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	url, has := env["WEBSITE_URL"]
	require.True(t, has, "WEBSITE_URL should be in environment after deploy")

	if session == nil {
		err = probeServiceHealth(
			t, ctx, http.DefaultClient, retry.NewConstant(5*time.Second), url, expectedTestAppResponse)
		require.NoError(t, err)
	} else {
		session.Variables[recording.SubscriptionIdKey] = env[environment.SubscriptionIdEnvVarName]

		err = probeServiceHealth(
			t, ctx, session.ProxyClient, retry.NewConstant(1*time.Millisecond), url, expectedTestAppResponse)
		require.NoError(t, err)
	}

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)

	// As part of deleting the infrastructure, outputs of the infrastructure such as "WEBSITE_URL" should
	// have been removed from the environment.
	env, err = godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	_, has = env["WEBSITE_URL"]
	require.False(t, has, "WEBSITE_URL should have been removed from the environment as part of infrastructure removal")
}

func Test_CLI_Up_ResourceGroupScope(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	session := recording.Start(t)
	client := http.DefaultClient
	subscriptionId := cfg.SubscriptionID
	if session != nil {
		client = session.ProxyClient

		if session.Playback {
			subscriptionId = session.Variables[recording.SubscriptionIdKey]
		}
	}

	if subscriptionId == "" {
		t.Skip("Skipping test because no subscription was provided")
	}

	envName := randomOrStoredEnvName(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	resourceGroupName := fmt.Sprintf("rg-%s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env,
		"AZURE_LOCATION=eastus2")

	cred := azdcli.NewTestCredential(cli)

	rgClient, err := armresources.NewResourceGroupsClient(subscriptionId, cred, &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: client,
		},
	})
	require.NoError(t, err)

	_, err = rgClient.CreateOrUpdate(context.Background(), resourceGroupName, armresources.ResourceGroup{
		Name:     to.Ptr(resourceGroupName),
		Location: to.Ptr("eastus2"),
		Tags: map[string]*string{
			"DeleteAfter": to.Ptr(time.Now().Add(60 * time.Minute).UTC().Format(time.RFC3339)),
		},
	}, nil)

	require.NoError(t, err)

	err = copySample(dir, "storage-rg")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "set", "AZURE_RESOURCE_GROUP", resourceGroupName)
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision")
	require.NoError(t, err)

	// The sample outputs the ID of the storage account it created, let's make sure that resource is in the resource
	// group we created (by looking at the resourceGroup component in the id)
	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	storageAccountId, has := env["STORAGE_ACCOUNT_ID"]
	require.True(t, has)

	require.Contains(t, strings.ToLower(storageAccountId), fmt.Sprintf("/resourcegroups/%s/", resourceGroupName))

	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = env[environment.SubscriptionIdEnvVarName]
	}

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}

type httpClient interface {
	Get(url string) (*http.Response, error)
}

// Validates that the service is up-and-running, by issuing a GET request
// and expecting a 2XX status code, with a matching response body.
func probeServiceHealth(
	t *testing.T,
	ctx context.Context,
	client httpClient,
	backOff retry.Backoff,
	url string,
	expectedBody string) error {
	return retry.Do(ctx, retry.WithMaxRetries(10, backOff), func(ctx context.Context) error {
		t.Logf("Attempting to Get URL: %s", url)

		/* #nosec G107 - Potential HTTP request made with variable url false positive */
		res, err := client.Get(url)
		if err != nil {
			return retry.RetryableError(err)
		}

		var buf bytes.Buffer
		_, err = buf.ReadFrom(res.Body)
		require.NoError(t, err)

		bodyString := buf.String()

		if bodyString != expectedBody {
			return retry.RetryableError(
				fmt.Errorf("expected %s but got %s for request to %s", expectedBody, bodyString, url),
			)
		} else {
			assert.Equal(t, expectedBody, bodyString)
			return nil
		}
	})
}
