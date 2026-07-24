// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestNewBicepCli(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	mockContext := mocks.NewMockContext(t.Context())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "downloads.bicep.azure.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("this is bicep")),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && len(args.Args) == 1 && args.Args[0] == "--version"
	}).Respond(exec.NewRunResult(
		0,
		fmt.Sprintf("Bicep CLI version %s (abcdef0123)", Version.String()),
		"",
	))

	cli := newCliWithTransporter(
		mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient,
	)
	err := cli.ensureInstalledOnce(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, cli)

	require.Equal(t, 2, len(mockContext.Console.SpinnerOps()))

	require.Equal(t, mockinput.SpinnerOp{
		Op:      mockinput.SpinnerOpShow,
		Message: "Downloading Bicep",
		Format:  input.Step,
	}, mockContext.Console.SpinnerOps()[0])

	require.Equal(t, mockinput.SpinnerOp{
		Op:      mockinput.SpinnerOpStop,
		Message: "Downloading Bicep",
		Format:  input.StepDone,
	}, mockContext.Console.SpinnerOps()[1])

	bicepPath, err := azdBicepPath()
	require.NoError(t, err)

	contents, err := os.ReadFile(bicepPath)
	require.NoError(t, err)

	require.Equal(t, []byte("this is bicep"), contents)
}

func TestNewBicepCliWillUpgrade(t *testing.T) {
	const OLD_FILE_CONTENTS = "this is OLD bicep"
	const NEW_FILE_CONTENTS = "this is NEW bicep"

	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	bicepPath, err := azdBicepPath()
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Dir(bicepPath), osutil.PermissionDirectory)
	require.NoError(t, err)

	err = os.WriteFile(bicepPath, []byte(OLD_FILE_CONTENTS), osutil.PermissionExecutableFile)
	require.NoError(t, err)

	mockContext := mocks.NewMockContext(t.Context())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "downloads.bicep.azure.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(NEW_FILE_CONTENTS)),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && len(args.Args) == 1 && args.Args[0] == "--version"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		contents, err := os.ReadFile(bicepPath)
		if err != nil {
			return exec.NewRunResult(-1, "", "couldn't read bicep file"), err
		}

		switch string(contents) {
		case OLD_FILE_CONTENTS:
			return exec.NewRunResult(0, "Bicep CLI version 0.0.1 (badbadbad1)", ""), nil
		case NEW_FILE_CONTENTS:
			return exec.NewRunResult(0, fmt.Sprintf("Bicep CLI version %s (abcdef0123)", Version.String()), ""), nil
		}

		return exec.NewRunResult(-1, "", "unexpected bicep file contents"), err
	})

	cli := newCliWithTransporter(
		mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient,
	)
	err = cli.ensureInstalledOnce(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, cli)

	require.Equal(t, 2, len(mockContext.Console.SpinnerOps()))

	require.Equal(t, mockinput.SpinnerOp{
		Op:      mockinput.SpinnerOpShow,
		Message: "Upgrading Bicep",
		Format:  input.Step,
	}, mockContext.Console.SpinnerOps()[0])

	require.Equal(t, mockinput.SpinnerOp{
		Op:      mockinput.SpinnerOpStop,
		Message: "Upgrading Bicep",
		Format:  input.StepDone,
	}, mockContext.Console.SpinnerOps()[1])

	contents, err := os.ReadFile(bicepPath)
	require.NoError(t, err)

	require.Equal(t, []byte(NEW_FILE_CONTENTS), contents)
}

func Test_preferMuslBicep(t *testing.T) {
	tests := []struct {
		name     string
		hasMusl  bool
		hasGlibc bool
		want     bool
	}{
		{
			name:     "musl preferred",
			hasMusl:  true,
			hasGlibc: false,
			want:     true,
		},
		{
			name:     "glibc preferred",
			hasMusl:  false,
			hasGlibc: true,
			want:     false,
		},
		{
			name:     "both available",
			hasMusl:  true,
			hasGlibc: true,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStat := func(name string) (os.FileInfo, error) {
				if tt.hasMusl && name == "/lib/ld-musl-x86_64.so.1" {
					return &fakeFileInfo{}, nil
				}
				if tt.hasGlibc && name == "/lib/x86_64-linux-gnu/libc.so.6" {
					return &fakeFileInfo{}, nil
				}

				return nil, os.ErrNotExist
			}
			got := preferMuslBicep(mockStat)
			assert.Equal(t, tt.want, got)
		})
	}
}

type fakeFileInfo struct {
}

func (f *fakeFileInfo) Name() string {
	return ""
}

func (f *fakeFileInfo) Size() int64 {
	return 0
}

func (f *fakeFileInfo) Mode() os.FileMode {
	return 0
}

func (f *fakeFileInfo) ModTime() time.Time {
	return time.Time{}
}

func (f *fakeFileInfo) IsDir() bool {
	return false
}

func (f *fakeFileInfo) Sys() any {
	return nil
}

// writeFakeBicep creates a fake bicep executable at a temp path and returns the path.
// It also sets AZD_BICEP_TOOL_PATH so ensureInstalled picks it up without downloading.
func writeFakeBicep(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "bicep"
	if filepath.Separator == '\\' {
		name = "bicep.exe"
	}
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte("fake"), 0o600))
	t.Setenv("AZD_BICEP_TOOL_PATH", p)
	// Also give a config dir so azdBicepPath never complains if queried elsewhere.
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	return p
}

func TestNewCli(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	cli := NewCli(mockContext.Console, mockContext.CommandRunner)
	require.NotNil(t, cli)
	require.NotNil(t, cli.runner)
	require.NotNil(t, cli.console)
	require.NotNil(t, cli.transporter)
}

func TestEnsureInstalled_ToolPathOverride(t *testing.T) {
	p := writeFakeBicep(t)

	mockContext := mocks.NewMockContext(t.Context())
	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)

	require.NoError(t, cli.ensureInstalledOnce(*mockContext.Context))
	require.Equal(t, p, cli.path)
	// No download spinner because override short-circuits.
	require.Equal(t, 0, len(mockContext.Console.SpinnerOps()))
}

func TestBuild_SuccessAndError(t *testing.T) {
	tests := []struct {
		name       string
		runErr     error
		runStdout  string
		runStderr  string
		wantErr    bool
		wantOutput string
		wantLint   string
	}{
		{
			name:       "success",
			runStdout:  `{"$schema":"arm"}`,
			runStderr:  "some lint warning",
			wantOutput: `{"$schema":"arm"}`,
			wantLint:   "some lint warning",
		},
		{
			name:    "failure",
			runErr:  errors.New("bicep exploded"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writeFakeBicep(t)

			mockContext := mocks.NewMockContext(t.Context())
			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return len(args.Args) >= 1 && args.Args[0] == "build"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				if tc.runErr != nil {
					return exec.NewRunResult(1, "", tc.runErr.Error()), tc.runErr
				}
				return exec.NewRunResult(0, tc.runStdout, tc.runStderr), nil
			})

			cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
			res, err := cli.Build(t.Context(), "main.bicep")
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "failed running bicep build")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantOutput, res.Compiled)
			require.Equal(t, tc.wantLint, res.LintErr)
		})
	}
}

func TestBuild_EnsureInstalledError(t *testing.T) {
	t.Setenv("AZD_BICEP_TOOL_PATH", "")
	// Force azdBicepPath failure by clearing config dir and disabling download.
	// Instead, force download failure using an HTTP 500.
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.Host == "downloads.bicep.azure.com"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	_, err := cli.Build(t.Context(), "main.bicep")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ensuring bicep is installed")
}

func TestBuildBicepParam_SuccessAndError(t *testing.T) {
	tests := []struct {
		name      string
		fail      bool
		env       []string
		wantOut   string
		wantErrLn string
	}{
		{name: "success-no-env", wantOut: "compiled", wantErrLn: ""},
		{name: "success-with-env", env: []string{"FOO=bar", "BAZ=qux"}, wantOut: "compiled2"},
		{name: "failure", fail: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writeFakeBicep(t)

			mockContext := mocks.NewMockContext(t.Context())
			var capturedArgs exec.RunArgs
			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return len(args.Args) >= 1 && args.Args[0] == "build-params"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				if tc.fail {
					return exec.NewRunResult(1, "", "nope"), errors.New("bad")
				}
				return exec.NewRunResult(0, tc.wantOut, tc.wantErrLn), nil
			})

			cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
			res, err := cli.BuildBicepParam(t.Context(), "main.bicepparam", tc.env)
			if tc.fail {
				require.Error(t, err)
				require.Contains(t, err.Error(), "failed running bicep build")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantOut, res.Compiled)
			require.Equal(t, []string{"build-params", "main.bicepparam", "--stdout"}, capturedArgs.Args)
			if tc.env != nil {
				require.Equal(t, tc.env, capturedArgs.Env)
			}
		})
	}
}

func TestSnapshotOptionsBuilder(t *testing.T) {
	t.Parallel()

	opts := NewSnapshotOptions().
		WithMode("validate").
		WithTenantID("tid").
		WithSubscriptionID("sid").
		WithResourceGroup("rg").
		WithLocation("eastus2").
		WithDeploymentName("dep")

	require.Equal(t, "validate", opts.Mode)
	require.Equal(t, "tid", opts.TenantID)
	require.Equal(t, "sid", opts.SubscriptionID)
	require.Equal(t, "rg", opts.ResourceGroup)
	require.Equal(t, "eastus2", opts.Location)
	require.Equal(t, "dep", opts.DeploymentName)

	// NewSnapshotOptions returns zero value.
	zero := NewSnapshotOptions()
	require.Equal(t, SnapshotOptions{}, zero)
}

func TestSnapshot_Success_AllOptionsAppendedAndFileRead(t *testing.T) {
	writeFakeBicep(t)

	dir := t.TempDir()
	paramFile := filepath.Join(dir, "main.bicepparam")
	require.NoError(t, os.WriteFile(paramFile, []byte("param"), 0o600))
	snapshotFile := filepath.Join(dir, "main.snapshot.json")

	mockContext := mocks.NewMockContext(t.Context())
	var capturedArgs []string
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) >= 1 && args.Args[0] == "snapshot"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args.Args
		// simulate bicep CLI producing the snapshot file
		require.NoError(t, os.WriteFile(snapshotFile, []byte(`{"snap":true}`), 0o600))
		return exec.NewRunResult(0, "", ""), nil
	})

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	opts := NewSnapshotOptions().
		WithMode("overwrite").
		WithTenantID("tid").
		WithSubscriptionID("sid").
		WithResourceGroup("rg").
		WithLocation("eastus").
		WithDeploymentName("dep")

	data, err := cli.Snapshot(t.Context(), paramFile, opts)
	require.NoError(t, err)
	require.Equal(t, []byte(`{"snap":true}`), data)

	// Ensure all flags were forwarded.
	joined := strings.Join(capturedArgs, " ")
	for _, exp := range []string{
		"snapshot", paramFile,
		"--mode overwrite",
		"--tenant-id tid",
		"--subscription-id sid",
		"--resource-group rg",
		"--location eastus",
		"--deployment-name dep",
	} {
		require.Contains(t, joined, exp)
	}

	// File should have been removed.
	_, statErr := os.Stat(snapshotFile)
	require.True(t, os.IsNotExist(statErr), "snapshot file should have been removed")
}

func TestSnapshot_Success_NoOptions(t *testing.T) {
	writeFakeBicep(t)

	dir := t.TempDir()
	paramFile := filepath.Join(dir, "main.bicepparam")
	require.NoError(t, os.WriteFile(paramFile, []byte("param"), 0o600))
	snapshotFile := filepath.Join(dir, "main.snapshot.json")

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) >= 1 && args.Args[0] == "snapshot"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		// Only "snapshot" and filename, no other flags.
		require.Equal(t, []string{"snapshot", paramFile}, args.Args)
		require.NoError(t, os.WriteFile(snapshotFile, []byte("x"), 0o600))
		return exec.NewRunResult(0, "", ""), nil
	})

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	data, err := cli.Snapshot(t.Context(), paramFile, NewSnapshotOptions())
	require.NoError(t, err)
	require.Equal(t, []byte("x"), data)
}

func TestSnapshot_CommandFails(t *testing.T) {
	writeFakeBicep(t)
	dir := t.TempDir()
	paramFile := filepath.Join(dir, "main.bicepparam")
	require.NoError(t, os.WriteFile(paramFile, []byte("param"), 0o600))

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) >= 1 && args.Args[0] == "snapshot"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "boom"), errors.New("exit 1")
	})

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	_, err := cli.Snapshot(t.Context(), paramFile, NewSnapshotOptions())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed running bicep snapshot")
}

func TestSnapshot_MissingSnapshotFile(t *testing.T) {
	writeFakeBicep(t)
	dir := t.TempDir()
	paramFile := filepath.Join(dir, "main.bicepparam")
	require.NoError(t, os.WriteFile(paramFile, []byte("param"), 0o600))

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) >= 1 && args.Args[0] == "snapshot"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		// Don't create the file.
		return exec.NewRunResult(0, "", ""), nil
	})

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	_, err := cli.Snapshot(t.Context(), paramFile, NewSnapshotOptions())
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading snapshot file")
}

func TestVersion_ParseFailure(t *testing.T) {
	writeFakeBicep(t)

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) == 1 && args.Args[0] == "--version"
	}).Respond(exec.NewRunResult(0, "not a version string", ""))

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	_, err := cli.version(t.Context())
	require.Error(t, err)
}

func TestVersion_RunnerFailure(t *testing.T) {
	writeFakeBicep(t)

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) == 1 && args.Args[0] == "--version"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "err"), errors.New("failed")
	})

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	_, err := cli.version(t.Context())
	require.Error(t, err)
}

// TestEnsureInstalled_RedownloadsOnVersionCheckFailure verifies that when a pre-existing managed
// bicep binary fails its version check (e.g. wrong architecture, corrupt), ensureInstalled
// re-downloads it once and succeeds if the fresh binary is functional.
func TestEnsureInstalled_RedownloadsOnVersionCheckFailure(t *testing.T) {
	const brokenBinary = "broken-wrong-arch-bicep"
	const freshBinary = "good-bicep-contents"

	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)
	t.Setenv("AZD_BICEP_TOOL_PATH", "")

	bicepPath, err := azdBicepPath()
	require.NoError(t, err)

	// Seed a "broken" pre-existing binary.
	require.NoError(t, os.MkdirAll(filepath.Dir(bicepPath), osutil.PermissionDirectory))
	require.NoError(t, os.WriteFile(bicepPath, []byte(brokenBinary), osutil.PermissionExecutableFile))

	mockContext := mocks.NewMockContext(t.Context())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "downloads.bicep.azure.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(freshBinary)),
	})

	// Version check fails for the broken binary but succeeds after re-download.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && len(args.Args) == 1 && args.Args[0] == "--version"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		contents, err := os.ReadFile(bicepPath)
		if err != nil {
			return exec.NewRunResult(-1, "", "read error"), err
		}
		switch string(contents) {
		case brokenBinary:
			return exec.NewRunResult(255, "", "qemu-x86_64: Could not open '/lib64/ld-linux-x86-64.so.2'"),
				errors.New("exit code: 255")
		case freshBinary:
			return exec.NewRunResult(0, fmt.Sprintf("Bicep CLI version %s (abcdef0123)", Version.String()), ""), nil
		}
		return exec.NewRunResult(-1, "", "unexpected binary contents"), nil
	})

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	err = cli.ensureInstalledOnce(*mockContext.Context)
	require.NoError(t, err)

	// Re-download spinner must have fired.
	ops := mockContext.Console.SpinnerOps()
	require.Equal(t, 2, len(ops))
	require.Equal(t, mockinput.SpinnerOp{
		Op:      mockinput.SpinnerOpShow,
		Message: "Downloading Bicep",
		Format:  input.Step,
	}, ops[0])
	require.Equal(t, mockinput.SpinnerOp{
		Op:      mockinput.SpinnerOpStop,
		Message: "Downloading Bicep",
		Format:  input.StepDone,
	}, ops[1])

	// The binary should now contain the fresh download.
	contents, err := os.ReadFile(bicepPath)
	require.NoError(t, err)
	require.Equal(t, []byte(freshBinary), contents)
}

// TestEnsureInstalled_RedownloadStillFails verifies that if the re-downloaded binary
// also fails its version check, ensureInstalled propagates the error.
func TestEnsureInstalled_RedownloadStillFails(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)
	t.Setenv("AZD_BICEP_TOOL_PATH", "")

	bicepPath, err := azdBicepPath()
	require.NoError(t, err)

	// Seed a pre-existing binary that will fail.
	require.NoError(t, os.MkdirAll(filepath.Dir(bicepPath), osutil.PermissionDirectory))
	require.NoError(t, os.WriteFile(bicepPath, []byte("broken"), osutil.PermissionExecutableFile))

	mockContext := mocks.NewMockContext(t.Context())

	// Re-download also returns a broken binary.
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "downloads.bicep.azure.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("still-broken")),
	})

	// Version check always fails.
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") && len(args.Args) == 1 && args.Args[0] == "--version"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(255, "", "boom"), errors.New("exit code: 255")
	})

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	err = cli.ensureInstalledOnce(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "checking bicep version")
}

func TestEnsureInstalled_VersionCheckFailure(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)
	// Ensure override is not set.
	t.Setenv("AZD_BICEP_TOOL_PATH", "")

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.Host == "downloads.bicep.azure.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("bicep-bytes")),
	})
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return len(args.Args) == 1 && args.Args[0] == "--version"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "boom"), errors.New("fail")
	})

	cli := newCliWithTransporter(mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient)
	err := cli.ensureInstalledOnce(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "checking bicep version")
}

func TestDownloadBicep_HttpError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bicep.out")

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.Host == "downloads.bicep.azure.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusTeapot,
		Body:       io.NopCloser(bytes.NewReader(nil)),
	})

	err := downloadBicep(t.Context(), mockContext.HttpClient, Version, target)
	require.Error(t, err)
	require.Contains(t, err.Error(), "http error")
}

func TestDownloadBicep_TransportError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bicep.out")

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return true
	}).SetNonRetriableError(fmt.Errorf("network is down"))

	err := downloadBicep(t.Context(), mockContext.HttpClient, Version, target)
	require.Error(t, err)
	require.Contains(t, err.Error(), "network is down")
}

func TestRunStep_ActionFailure(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	err := runStep(*mockContext.Context, mockContext.Console, "Working", func() error {
		return errors.New("oh no")
	})
	require.Error(t, err)
	ops := mockContext.Console.SpinnerOps()
	require.Equal(t, 2, len(ops))
	// The last op must be the failed-stop.
	require.Equal(t, "Working", ops[1].Message)
}

func TestAzdBicepPath(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	p, err := azdBicepPath()
	require.NoError(t, err)
	require.NotEmpty(t, p)
	require.True(t, strings.HasSuffix(p, "bicep") || strings.HasSuffix(p, "bicep.exe"))
}
