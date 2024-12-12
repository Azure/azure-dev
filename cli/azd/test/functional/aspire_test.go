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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/bradleyjkemp/cupaloy/v2"
	"github.com/stretchr/testify/require"
)

// The tests in this file is structured in such a way that:
//
// (fast) go test -run ^Test_CLI_Aspire_DetectGen - Detection + generation acceptance tests.
// (slow, > 10 mins) go test -run ^Test_CLI_Aspire_Deploy -timeout 30m - Full deployment acceptance tests.
// (all) go test -run ^Test_CLI_Aspire -timeout 30m - Runs all tests.

// Test_CLI_Aspire_DetectGen tests the detection and generation of an Aspire project.
func Test_CLI_Aspire_DetectGen(t *testing.T) {

	sn := snapshot.NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(""))
	snRoot := filepath.Join("testdata", "snaps", "aspire-full")

	t.Run("ManifestGen", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := newTestContext(t)
		defer cancel()

		dir := t.TempDir()
		// avoid symlinked paths as this may result in the final path returned
		// to be a valid, but aliased path to the absolute entries in the test,
		// which fails the test's path equality assertions.
		//
		// This issue occurs on macOS runner where TempDir returned is symlinked to /private/var.
		dir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)

		t.Logf("DIR: %s", dir)

		envName := randomEnvName()
		t.Logf("AZURE_ENV_NAME: %s", envName)

		err = copySample(dir, "aspire-full")
		require.NoError(t, err, "failed expanding sample")

		dotnetCli := dotnet.NewCli(exec.NewCommandRunner(nil))
		appHostProject := filepath.Join(dir, "AspireAzdTests.AppHost")
		manifestPath := filepath.Join(appHostProject, "manifest.json")

		err = dotnetCli.PublishAppHostManifest(ctx, appHostProject, manifestPath, "")
		require.NoError(t, err)

		manifestFolder := filepath.Dir(manifestPath)
		// Snapshot every json and bicep under manifest path
		err = filepath.WalkDir(manifestFolder, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			fileName := d.Name()
			fileExt := filepath.Ext(fileName)
			parentFolder := filepath.Base(filepath.Dir(path))
			manifestFolderName := filepath.Base(manifestFolder)
			if !d.IsDir() && parentFolder == manifestFolderName && (fileExt == ".bicep" || fileName == "manifest.json") {
				return snapshotFile(sn, snRoot, manifestFolder, path)
			}

			return nil
		})
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

		// Clean relative paths to account for OS differences
		for svc := range prj.Services {
			prj.Services[svc].RelativePath = filepath.Clean(prj.Services[svc].RelativePath)
		}

		for svc := range old.Services {
			old.Services[svc].RelativePath = filepath.Clean(old.Services[svc].RelativePath)
		}

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
		cli.Env = append(cli.Env, os.Environ()...)
		//nolint:lll
		cli.Env = append(cli.Env, "AZD_ALPHA_ENABLE_INFRASYNTH=true")

		_, err = cli.RunCommand(ctx, "infra", "synth")
		require.NoError(t, err)

		bicepCli, err := bicep.NewCli(ctx, mockinput.NewMockConsole(), exec.NewCommandRunner(nil))
		require.NoError(t, err)

		// Validate bicep builds without errors
		// cdk lint errors are expected
		_, err = bicepCli.Build(ctx, filepath.Join(dir, "infra", "main.bicep"))
		require.NoError(t, err)

		// Snapshot everything under infra and manifests
		err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			parentDir := filepath.Base(filepath.Dir(path))
			fileExt := filepath.Ext(path)
			if !d.IsDir() && parentDir == "infra" || parentDir == "manifests" || fileExt == ".bicep" {
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

	dir, err := os.MkdirTemp("", "aspire-deploy")
	require.NoError(t, err)
	t.Logf("DIR: %s", dir)
	defer func() {
		if !cfg.CI && t.Failed() {
			t.Logf("kept directory %s for troubleshooting", dir)
			return
		}

		err = os.RemoveAll(dir)
		if err != nil {
			t.Logf("failed to remove %s", dir)
		}
	}()

	session := recording.Start(t)
	envName := randomOrStoredEnvName(session)
	_ = cfgOrStoredSubscription(session)
	t.Logf("AZURE_ENV_NAME: %s", envName)

	err = copySample(dir, "aspire-full")
	require.NoError(t, err, "failed expanding sample")

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	defer cleanupDeployments(ctx, t, cli, session, envName)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "up")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}

// cleanupDeployments deletes all subscription level deployments tagged with `azd-env-name` equal to envName. If the session
// indcates we are in playback mode, this function is a no-op.
func cleanupDeployments(ctx context.Context, t *testing.T, azCLI *azdcli.CLI, session *recording.Session, envName string) {
	if session != nil && session.Playback {
		return
	}

	client, err := armresources.NewDeploymentsClient(cfg.SubscriptionID, azdcli.NewTestCredential(azCLI), nil)
	if err != nil {
		return
	}

	pager := client.NewListAtSubscriptionScopePager(nil)
	var deploymentNames []string

	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			t.Logf("cleanupDeployments: failed to list next deployments page: %v", err)
			break
		}

		for _, deployment := range resp.Value {
			if deployment != nil && deployment.Tags != nil {
				tagVal := deployment.Tags[azure.TagKeyAzdEnvName]
				if tagVal != nil && *tagVal == envName {
					deploymentNames = append(deploymentNames, *deployment.Name)
				}
			}
		}
	}

	for _, deploymentName := range deploymentNames {
		t.Logf("cleanupDeployments: deleting deployment %s", deploymentName)
		_, err := client.BeginDeleteAtSubscriptionScope(ctx, deploymentName, nil)
		if err != nil {
			t.Logf("cleanupDeployments: failed to delete deployment %s: %v", deploymentName, err)
		}
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

	// normalize line endings
	contents = []byte(strings.ReplaceAll(string(contents), "\r\n", "\n"))

	err = sn.
		WithOptions(cupaloy.SnapshotSubdirectory(filepath.Join(snapshotRoot, relDir))).
		SnapshotWithName(filepath.Base(targetPath), string(contents))
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Join(relDir, filepath.Base(targetPath)), err)
	}

	return nil
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
