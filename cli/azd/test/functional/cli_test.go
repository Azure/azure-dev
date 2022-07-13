// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/joho/godotenv"
	"github.com/sethvargo/go-retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_CLI_Login_FailsIfNoAzCliIsMissing(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = filterEnviron("PATH")

	out, err := cli.RunCommandWithStdIn(ctx, "", "login")
	require.Error(t, err)
	require.Contains(t, out, "Azure CLI is not installed, please see https://aka.ms/azure-dev/azure-cli-install to install")
}

func Test_CLI_Version_PrintsVersion(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	cli := azdcli.NewCLI(t)
	out, err := cli.RunCommand(ctx, "version")
	require.NoError(t, err)

	rn := os.Getenv("GITHUB_RUN_NUMBER")
	if rn != "" {
		// TODO: This should be read from cli/version.txt (https://github.com/Azure/azure-dev/issues/36)
		require.Contains(t, out, "0.1.0")
	} else {
		require.Contains(t, out, fmt.Sprintf("azd version %s", internal.Version))
	}
}

func Test_CLI_Init_FailsIfAzCliIsMissing(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	cli.Env = filterEnviron("PATH")

	out, err := cli.RunCommandWithStdIn(ctx, "", "init")
	require.Error(t, err)

	require.Contains(t, out, "Azure CLI is not installed, please see https://aka.ms/azure-dev/azure-cli-install to install")
}

func Test_CLI_Init_AsksForSubscriptionIdAndCreatesEnvAndProjectFile(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	_, err := cli.RunCommandWithStdIn(ctx, "Empty Template\nTESTENV\n\nOther (enter manually)\nMY_SUB_ID\n", "init")
	require.NoError(t, err)

	file, err := ioutil.ReadFile(getTestEnvPath(dir, "TESTENV"))

	require.NoError(t, err)

	require.Regexp(t, regexp.MustCompile(`AZURE_SUBSCRIPTION_ID="MY_SUB_ID"`+"\n"), string(file))
	require.Regexp(t, regexp.MustCompile(`AZURE_ENV_NAME="TESTENV"`+"\n"), string(file))

	proj, err := project.LoadProjectConfig(filepath.Join(dir, environment.ProjectFileName), &environment.Environment{})
	require.NoError(t, err)

	require.Equal(t, filepath.Base(dir), proj.Name)
}

func Test_CLI_Init_CanUseTemplate(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	_, err := cli.RunCommandWithStdIn(ctx, "TESTENV\n\nOther (enter manually)\nMY_SUB_ID\n", "init", "--template", "cosmos-dotnet-core-todo-app")
	require.NoError(t, err)

	// While `init` uses git behind the scenes to pull a template, we don't want to bring the history over or initialize a git
	// repository.
	require.NoDirExists(t, filepath.Join(dir, ".git"))

	// Ensure the project was initialized from the template by checking that a file from the template is present.
	require.FileExists(t, filepath.Join(dir, "README.md"))
}

func Test_CLI_InfraCreateAndDelete(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()
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

	file, err := ioutil.ReadFile(filepath.Join(dir, environment.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	// AZURE_STORAGE_ACCOUNT_NAME is an output of the template, make sure it was added to the .env file.
	// (the name of the resource is ${replace(envName, '-', '')}sa).
	require.Regexp(t, regexp.MustCompile(fmt.Sprintf(`AZURE_STORAGE_ACCOUNT_NAME="%sstore"`, strings.ReplaceAll(envName, "-", ""))), string(file))

	// Using `down` here to test the down alias to infra delete
	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}

func Test_CLI_InfraCreateAndDeleteWebApp(t *testing.T) {
	t.Skip("flakiness in app service preventing infrastructure creation")

	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()
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

	_, err = cli.RunCommand(ctx, "infra", "create")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "deploy")
	require.NoError(t, err)

	// The sample hosts a small application that just responds with a 200 OK with a body of "Hello, `azd`."
	// (without the quotes). Validate that the application is working.
	env, err := godotenv.Read(filepath.Join(dir, environment.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	url, has := env["WEBSITE_URL"]
	require.True(t, has, "WEBSITE_URL should be in environment after infra create")

	res, err := http.Get(url)
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = buf.ReadFrom(res.Body)
	require.NoError(t, err)

	assert.Equal(t, "Hello, `azd`.", buf.String())

	// Ensure `env refresh` works by removing an output parameter from the .env file and ensure that `env refresh`
	// brings it back.
	delete(env, "WEBSITE_URL")
	err = godotenv.Write(env, filepath.Join(dir, environment.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "env", "refresh")
	require.NoError(t, err)

	env, err = godotenv.Read(filepath.Join(dir, environment.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	_, has = env["WEBSITE_URL"]
	require.True(t, has, "WEBSITE_URL should be in environment after refresh")

	_, err = cli.RunCommand(ctx, "infra", "delete", "--force", "--purge")
	require.NoError(t, err)
}

// test for azd deploy, azd deploy --service
func Test_CLI_DeployInvalidName(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()
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
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()
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

func Test_CLI_InfraCreateAndDeleteFuncApp(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()
	t.Logf("DIR: %s", dir)

	envName := randomEnvName()
	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(os.Environ(), "AZURE_LOCATION=eastus2")

	err := copySample(dir, "funcapp")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForTests(envName), "init")
	require.NoError(t, err)

	t.Logf("Starting infra create\n")
	err = cmd.Execute([]string{"infra", "create", "--cwd", dir})
	require.NoError(t, err)

	t.Logf("Starting deploy\n")
	err = cmd.Execute([]string{"deploy", "--cwd", dir})
	require.NoError(t, err)

	fnName := envName + "func"
	url := fmt.Sprintf("https://%s.azurewebsites.net/api/httptrigger", fnName)

	t.Logf("Issuing GET request to function\n")

	// We've seen some cases in CI where issuing a get right after a deploy ends up with us getting a 404, so retry the request a
	// handful of times if it fails with a 404.
	err = retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(5*time.Second)), func(ctx context.Context) error {
		res, err := http.Get(url)
		if err != nil {
			return retry.RetryableError(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return retry.RetryableError(fmt.Errorf("expected %d but got %d for request to %s", http.StatusOK, res.StatusCode, url))
		}
		return nil
	})
	require.NoError(t, err)

	t.Logf("Starting infra delete\n")
	err = cmd.Execute([]string{"infra", "delete", "--cwd", dir, "--force", "--purge"})
	require.NoError(t, err)

	t.Logf("Done\n")
}

func Test_ProjectIsNeeded(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := t.TempDir()
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
		args := []string{"--cwd", dir}
		args = append(args, strings.Split(test.command, " ")...)
		if len(test.args) > 0 {
			args = append(args, test.args...)
		}

		t.Run(test.command, func(t *testing.T) {
			out, err := cli.RunCommand(ctx, args...)
			assert.Error(t, err)
			assert.Regexp(t, "no project exists; to create a new project, run `azd init`", out)
		})
	}
}

func Test_NoDebugSpewWhenHelpPassedWithoutDebug(t *testing.T) {
	stdErrBuf := bytes.Buffer{}

	cmd := exec.Command(azdcli.GetAzdLocation(), "--help")
	cmd.Stderr = &stdErrBuf

	// Run the command and wait for it to complete, we don't expect any errors.
	err := cmd.Start()
	assert.NoError(t, err)

	err = cmd.Wait()
	assert.NoError(t, err)

	// Ensure no output was written to stderr
	assert.Equal(t, "", stdErrBuf.String(), "no output should be written to stderr when --help is passed")
}

// filterEnviron returns a new copy of os.Environ after removing all specified keys, ignoring case.
func filterEnviron(toExclude ...string) []string {
	old := os.Environ()
	new := make([]string, 0, len(old))
	for _, val := range old {
		lowerVal := strings.ToLower(val)
		keep := true
		for _, exclude := range toExclude {
			if strings.HasPrefix(lowerVal, strings.ToLower(exclude)+"=") {
				keep = false
				break
			}
		}
		if keep {
			new = append(new, val)
		}
	}

	return new
}

// copySample copies the tree rooted at ${ROOT}/test/samples/ to targetRoot.
func copySample(targetRoot string, sampleName string) error {
	sampleRoot := filepath.Join(filepath.Dir(azdcli.GetAzdLocation()), "test", "samples", sampleName)

	return filepath.WalkDir(sampleRoot, func(name string, info fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		targetPath := filepath.Join(targetRoot, name[len(sampleRoot):])

		if info.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		contents, err := ioutil.ReadFile(name)
		if err != nil {
			return fmt.Errorf("reading sample file: %w", err)
		}
		return ioutil.WriteFile(targetPath, contents, 0644)
	})
}

func randomEnvName() string {
	bytes := make([]byte, 4)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(fmt.Errorf("could not read random bytes: %w", err))
	}

	return ("azdtest-" + hex.EncodeToString(bytes))[0:15]
}

// stdinForTests is just enough stdin to bypass all the prompts or choose defaults.
func stdinForTests(envName string) string {
	return fmt.Sprintf("%s\n", envName) + // "enter deployment name"
		"\n" + // "choose location" (we're choosing the default)
		"\n" // "choose subscription" (we're choosing the default)
}

func getTestEnvPath(dir string, envName string) string {
	return filepath.Join(dir, environment.EnvironmentDirectoryName, envName, ".env")
}

// newTestContext returns a new empty context, suitable for use in tests. If a
// the provided `testing.T` has a deadline applied, the returned context
// respects the deadline.
func newTestContext(t *testing.T) (context.Context, context.CancelFunc) {
	if deadline, ok := t.Deadline(); ok {
		return context.WithDeadline(context.Background(), deadline)
	}

	return context.WithCancel(context.Background())
}
