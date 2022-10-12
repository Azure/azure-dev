// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package webapp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/functional"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/joho/godotenv"
	"github.com/sethvargo/go-retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// TODO(azure-dev#834): When we remove the t.Skip we should be able
	// to remove this block. Right now it exists to appease some linters
	_ = newRunArgs("")
}

func newRunArgs(cmd string, args ...string) exec.RunArgs {
	runArgs := exec.NewRunArgs(cmd, args...)
	return runArgs.WithEnrichError(true)
}

func Test_CLI_InfraCreateAndDeleteWebApp(t *testing.T) {
	t.Skip("azure-dev/834")
	t.Setenv("AZURE_LOCATION", "eastus2")

	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)

	err := functional.CopySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	envName, initResp := functional.NewRandomNameEnvAndInitResponse(t)

	_, _, err = functional.RunCliCommandWithStdIn(t, ctx, initResp, "init")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "infra", "create")
	require.NoError(t, err)

	out, _, err := functional.RunCliCommand(t, ctx, "show", "-o", "json")
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
	err = json.Unmarshal([]byte(out), &showRes)
	require.NoError(t, err)

	service, has := showRes.Services["web"]
	require.True(t, has)
	require.Equal(t, "dotnet", service.Project.Language)
	require.Equal(t, "webapp.csproj", filepath.Base(service.Project.Path))
	require.Equal(t, 1, len(service.Target.ResourceIds))

	_, _, err = functional.RunCliCommand(t, ctx, "deploy")
	require.NoError(t, err)

	// The sample hosts a small application that just responds with a 200 OK with a body of "Hello, `azd`."
	// (without the quotes). Validate that the application is working.
	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	url, has := env["WEBSITE_URL"]
	require.True(t, has, "WEBSITE_URL should be in environment after infra create")

	// Add a retry here because appService deployment can take time
	err = retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(5*time.Second)), func(ctx context.Context) error {
		t.Logf("Attempting to Get URL: %s", url)

		res, err := http.Get(url)
		if err != nil {
			return retry.RetryableError(err)
		}

		var buf bytes.Buffer
		_, err = buf.ReadFrom(res.Body)
		require.NoError(t, err)

		testString := "Hello, `azd`."
		bodyString := buf.String()

		if bodyString != testString {
			return retry.RetryableError(fmt.Errorf("expected %s but got %s for request to %s", testString, bodyString, url))
		} else {
			assert.Equal(t, testString, bodyString)
			return nil
		}
	})
	require.NoError(t, err)

	commandRunner := exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
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

	_, _, err = functional.RunCliCommand(t, ctx, "env", "refresh")
	require.NoError(t, err)

	env, err = godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	_, has = env["WEBSITE_URL"]
	require.True(t, has, "WEBSITE_URL should be in environment after refresh")

	_, _, err = functional.RunCliCommand(t, ctx, "infra", "delete", "--force", "--purge")
	require.NoError(t, err)

	out, _, err = functional.RunCliCommand(t, ctx, "show", "-o", "json", "--cwd", dir)
	require.NoError(t, err)

	err = json.Unmarshal([]byte(out), &showRes)
	require.NoError(t, err)

	// Project information should be present, but since we have run infra delete, there shouldn't
	// be any resource ids.
	service, has = showRes.Services["web"]
	require.True(t, has)
	require.Equal(t, "dotnet", service.Project.Language)
	require.Equal(t, "webapp.csproj", filepath.Base(service.Project.Path))
	require.Nil(t, service.Target.ResourceIds)
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
