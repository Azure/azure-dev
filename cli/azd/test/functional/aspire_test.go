// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/bradleyjkemp/cupaloy/v2"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

// The tests in this file is structured in such a way that:
//
// (fast) go test -run ^Test_CLI_Aspire_DetectGen - Detection + generation acceptance tests.
// (slow, > 10 mins) go test -run ^Test_CLI_Aspire_Deploy -timeout 30m - Full deployment acceptance tests.
// (all) go test -run ^Test_CLI_Aspire - Runs all tests.

// Test_CLI_Aspire_DetectGen tests the detection and generation of an Aspire project.
func Test_CLI_Aspire_DetectGen(t *testing.T) {
	sn := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(""))
	snRoot := filepath.Join("testdata", "snaps", "aspire-full")

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

		err = snapshotFile(sn, snRoot, dir, manifestPath)
		require.NoError(t, err)
	})

	t.Run("Init", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		dir := tempDirWithDiagnostics(t)

		// create subdirectory with a known name
		dir = filepath.Join(dir, "AspireAzdTests")
		err := os.MkdirAll(dir, 0755)
		require.NoError(t, err, "failed creating temp dir")
		t.Logf("DIR: %s", dir)

		envName := randomEnvName()
		t.Logf("AZURE_ENV_NAME: %s", envName)

		err = copySample(dir, "aspire-full")
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

		require.Equal(t, prj.Services, old.Services)
	})

	t.Run("InfraSynth", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		dir := tempDirWithDiagnostics(t)
		t.Logf("DIR: %s", dir)

		// create subdirectory with a known name
		dir = filepath.Join(dir, "AspireAzdTests")
		err := os.MkdirAll(dir, 0755)
		require.NoError(t, err, "failed creating temp dir")
		t.Logf("DIR: %s", dir)

		envName := randomEnvName()
		t.Logf("AZURE_ENV_NAME: %s", envName)

		err = copySample(dir, "aspire-full")
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

		// Snapshot everything under infra and manifests
		err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			parentDir := filepath.Base(filepath.Dir(path))
			if !d.IsDir() && parentDir == "infra" || parentDir == "manifests" {
				return snapshotFile(sn, snRoot, dir, path)
			}

			return nil
		})
		require.NoError(t, err)
	})
}

// Test_CLI_Aspire_Deploy tests the full deployment of an Aspire project.
func Test_CLI_Aspire_Deploy(t *testing.T) {
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
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx,
		"n\n"+ // Don't expose 'apiservice' service.
			"y\n"+ // Expose 'webfrontend' service.
			stdinForProvision(), "up")
	require.NoError(t, err)

	env, err := godotenv.Read(filepath.Join(dir, azdcontext.EnvironmentDirectoryName, envName, ".env"))
	require.NoError(t, err)

	domain, has := env["AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN"]
	require.True(t, has, "AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN should be in environment after deploy")

	endpoint := fmt.Sprintf("https://%s.%s", "webfrontend", domain)
	runLiveDotnetPlaywright(t, ctx, filepath.Join(dir, "AspireAzdTests"), endpoint)

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}

func runLiveDotnetPlaywright(
	t *testing.T,
	ctx context.Context,
	projDir string,
	endpoint string) {
	runner := exec.NewCommandRunner(nil)
	run := func() (res exec.RunResult, err error) {
		wr := logWriter{initialTime: time.Now(), t: t, prefix: "webfrontend: "}
		res, err = runner.Run(ctx, exec.NewRunArgs(
			"dotnet",
			"test",
			"--logger",
			"console;verbosity=detailed",
		).WithCwd(projDir).WithEnv(append(
			os.Environ(),
			"LIVE_APP_URL="+endpoint,
		)).WithStdOut(&wr))
		return
	}

	res, err := run()
	if err != nil && strings.Contains(res.Stdout, "Permission denied") {
		// recover from permission denied error
		err := filepath.WalkDir(projDir, func(path string, d os.DirEntry, err error) error {
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
}

// Snapshots a file located at targetPath. Saves the snapshot to snapshotRoot/rel, where rel is relative to targetRoot.
func snapshotFile(
	sn *cupaloy.Config,
	snapshotRoot string,
	targetRoot string,
	targetPath string) error {
	relDir, err := filepath.Rel(targetRoot, filepath.Dir(targetPath))
	if err != nil {
		return err
	}

	contents, err := os.ReadFile(targetPath)
	if err != nil {
		return err
	}

	return sn.
		WithOptions(cupaloy.SnapshotSubdirectory(filepath.Join(snapshotRoot, relDir))).
		SnapshotWithName(filepath.Base(targetPath), string(contents))
}

type logWriter struct {
	t           *testing.T
	sb          strings.Builder
	prefix      string
	initialTime time.Time
}

func (l *logWriter) Write(bytes []byte) (n int, err error) {
	for i, b := range bytes {
		err = l.sb.WriteByte(b)
		if err != nil {
			return i, err
		}

		if b == '\n' {
			l.t.Logf("%s %s%s", time.Since(l.initialTime).Round(1*time.Millisecond), l.prefix, l.sb.String())
			l.sb.Reset()
		}
	}
	return len(bytes), nil
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
