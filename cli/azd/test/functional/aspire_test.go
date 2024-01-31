// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/stretchr/testify/require"
)

func testDataDir() string {
	_, b, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(b), "testdata")
}

func Test_CLI_Aspire(t *testing.T) {
	t.Run("ManifestGen", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		dir := t.TempDir()
		t.Logf("DIR: %s", dir)

		envName := randomEnvName()
		t.Logf("AZURE_ENV_NAME: %s", envName)

		err := copySample(dir, "aspire-full")
		require.NoError(t, err, "failed expanding sample")

		dotnetCli := dotnet.NewDotNetCli(exec.NewCommandRunner(nil))
		appHostProject := filepath.Join(dir, "AspireAzdTests.AppHost")
		manifestPath := filepath.Join(appHostProject, "manifest.json")
		err = dotnetCli.PublishAppHostManifest(ctx, appHostProject, manifestPath)
		require.NoError(t, err)

		manifest, err := os.ReadFile(manifestPath)
		require.NoError(t, err)

		snapshot.SnapshotT(t, manifest)
	})

	t.Run("Init", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		dir := tempDirWithDiagnostics(t)
		t.Logf("DIR: %s", dir)

		envName := randomEnvName()
		t.Logf("AZURE_ENV_NAME: %s", envName)

		err := copySample(dir, "aspire-full")
		require.NoError(t, err, "failed expanding sample")

		// Rename existing azure.yaml that is included with the sample
		err = os.Rename(filepath.Join(dir, "azure.yaml"), filepath.Join(dir, "azure.yaml.old"))
		require.NoError(t, err, "renaming azure.yaml")

		cli := azdcli.NewCLI(t)
		cli.WorkingDirectory = dir

		initResponses := []string{
			"Use code in the current directory",        // Initialize from code
			"Confirm and continue initializing my app", // Confirm everything looks good.
			"n",     // Don't expose 'apiservice' service.
			"y",     // Expose 'webfrontend' service.
			envName, // The name of the environment to create.
			"",      // ensure a trailing newline when we join each of these lines together.
		}
		_, err = cli.RunCommandWithStdIn(ctx, strings.Join(initResponses, "\n"), "init")
		require.NoError(t, err)

		require.FileExists(t, filepath.Join(dir, "azure.yaml"))

		prj, err := project.Load(ctx, filepath.Join(dir, "azure.yaml"))
		require.NoError(t, err)

		old, err := project.Load(ctx, filepath.Join(dir, "azure.yaml.old"))
		require.NoError(t, err)

		// clear out name for comparison purposes
		prj.Name = ""
		old.Name = ""

		require.Equal(t, prj.Services, old.Services)

	})

	t.Run("InfraSynth", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		dir := tempDirWithDiagnostics(t)
		t.Logf("DIR: %s", dir)

		envName := randomEnvName()
		t.Logf("AZURE_ENV_NAME: %s", envName)

		err := copySample(dir, "aspire-full")
		require.NoError(t, err, "failed expanding sample")

		cli := azdcli.NewCLI(t)
		cli.WorkingDirectory = dir

		_, err = cli.RunCommand(ctx, "infra", "synth")
		require.NoError(t, err)

		bicepCli, err := bicep.NewBicepCli(ctx, mockinput.NewMockConsole(), exec.NewCommandRunner(nil))
		require.NoError(t, err)

		// Validate bicep builds without errors or lint errors
		res, err := bicepCli.Build(ctx, filepath.Join(dir, "infra", "main.bicep"))
		require.NoError(t, err)
		lintErr := lintErr(
			res,
			[]string{"Warning no-unused-params: Parameter \"inputs\" is declared but never used."})
		require.Len(t, lintErr, 0, "lint errors occurred")
	})

	t.Run("Up", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		dir := tempDirWithDiagnostics(t)
		t.Logf("DIR: %s", dir)

		session := recording.Start(t)

		envName := randomOrStoredEnvName(session)
		t.Logf("AZURE_ENV_NAME: %s", envName)

		err := copySample(dir, "aspire-full")
		require.NoError(t, err, "failed expanding sample")

		cli := azdcli.NewCLI(t)
		cli.WorkingDirectory = dir
		cli.Env = append(cli.Env, os.Environ()...)
		cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

		// _, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
		// require.NoError(t, err)

		// _, err = cli.RunCommandWithStdIn(ctx,
		// 	"n\n"+ // Don't expose 'apiservice' service.
		// 		"y\n"+ // Expose 'webfrontend' service.
		// 		stdinForProvision(), "up")
		// require.NoError(t, err)

		// env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
		// require.NoError(t, err)

		// domain, has := env["AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN"]
		// require.True(t, has, "AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN should be in environment after deploy")

		// if session != nil {
		// 	session.Variables[recording.SubscriptionIdKey] = env["AZURE_SUBSCRIPTION_ID"]
		// }

		endpoint := fmt.Sprintf("https://%s.%s", "webfrontend", "lemonsand-4c6ed263.eastus2.azurecontainerapps.io")
		//endpoint := fmt.Sprintf("https://%s.%s", "webfrontend", domain)
		runner := exec.NewCommandRunner(nil)
		run := func() (res exec.RunResult, err error) {
			res, err = runner.Run(ctx, exec.NewRunArgs(
				"dotnet",
				"test",
				"--logger",
				"console;verbosity=detailed",
			).WithCwd(filepath.Join(dir, "AspireAzdTests")).WithEnv([]string{
				"LIVE_APP_URL=" + endpoint,
			}))
			t.Log(res.Stdout)
			return
		}
		res, err := run()
		if err != nil && strings.Contains(res.Stdout, "Permission denied") {
			err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}

				if !d.IsDir() && d.Name() == "playwright.sh" {
					return os.Chmod(path, 0700)
				}

				return nil
			})
			if err != nil {
				t.Fatal(err)
			}

			res, err = run()
			require.NoError(t, err)
		}

		_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
		require.NoError(t, err)
	})
}

func lintErr(buildRes bicep.BuildResult, exclude []string) []string {
	var ret []string
	for _, s := range strings.Split(buildRes.LintErr, "\n") {
		for _, e := range exclude {
			if len(s) > 0 && !strings.Contains(s, e) {
				ret = append(ret, s)
			}
		}
	}
	return ret
}
