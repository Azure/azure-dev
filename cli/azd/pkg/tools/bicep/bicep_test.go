package bicep

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBicepCli(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	mockContext := mocks.NewMockContext(context.Background())

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
		fmt.Sprintf("Bicep CLI version %s (abcdef0123)", BicepVersion.String()),
		"",
	))

	cli, err := newBicepCliWithTransporter(
		*mockContext.Context, mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient,
	)
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

	mockContext := mocks.NewMockContext(context.Background())

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
			return exec.NewRunResult(0, fmt.Sprintf("Bicep CLI version %s (abcdef0123)", BicepVersion.String()), ""), nil
		}

		return exec.NewRunResult(-1, "", "unexpected bicep file contents"), err
	})

	cli, err := newBicepCliWithTransporter(
		*mockContext.Context, mockContext.Console, mockContext.CommandRunner, mockContext.HttpClient,
	)
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

func (f *fakeFileInfo) Sys() interface{} {
	return nil
}
