package pack

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockzip"
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
)

func Test_extractZip(t *testing.T) {
	const contentZipped = "zipped pack"

	tests := []struct {
		name    string
		files   []mockzip.File
		wantErr bool
	}{
		{
			"found",
			[]mockzip.File{
				{
					Name:    path.Join("bin", packName()),
					Content: contentZipped,
				},
				{
					Name: "bin/etc",
				},
				{
					Name: "etc",
				},
			},
			false,
		},
		{
			"not found",
			[]mockzip.File{
				{
					Name: "bin/etc",
				},
				{
					Name: "etc",
				},
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			zip, err := mockzip.Zip(tt.files)
			require.NoError(t, err)

			file := filepath.Join(dir, "pack.zip")
			err = os.WriteFile(file, zip.Bytes(), 0600)
			require.NoError(t, err)

			packCli, err := extractCli(file, dir)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				content, err := os.ReadFile(packCli)

				require.NoError(t, err)
				require.EqualValues(t, []byte(contentZipped), content)
			}
		})
	}
}

func Test_extractTgz(t *testing.T) {
	const contentZipped = "gzipped pack"

	tests := []struct {
		name    string
		files   []mockzip.File
		wantErr bool
	}{
		{
			"found",
			[]mockzip.File{
				{
					Name:    "bin/pack",
					Content: contentZipped,
				},
				{
					Name: "bin/etc",
				},
				{
					Name: "etc",
				},
			},
			false,
		},
		{
			"not found",
			[]mockzip.File{
				{
					Name: "bin/etc",
				},
				{
					Name: "etc",
				},
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			zip, err := mockzip.GzippedTar(tt.files)
			require.NoError(t, err)

			file := filepath.Join(dir, "pack.tgz")
			err = os.WriteFile(file, zip.Bytes(), 0600)
			require.NoError(t, err)

			packCli, err := extractCli(file, dir)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				content, err := os.ReadFile(packCli)

				require.NoError(t, err)
				require.EqualValues(t, []byte(contentZipped), content)
			}
		})
	}
}

func TestNewPackCliInstall(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	mockContext := mocks.NewMockContext(context.Background())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "github.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("pack cli")),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.HasSuffix(args.Cmd, packName()) && args.Args[0] == "--version" && len(args.Args) == 1
	}).Respond(exec.NewRunResult(
		0,
		fmt.Sprintf("%s+git-c38f7da.build-4952", PackVersion.String()),
		"",
	))

	mockExtract := func(src, dst string) (string, error) {
		exp, _ := packCliPath()
		_ = osutil.Rename(context.Background(), src, exp)
		return src, nil
	}

	cli, err := newPackCliImpl(
		*mockContext.Context,
		mockContext.Console,
		mockContext.CommandRunner,
		mockContext.HttpClient,
		mockExtract,
	)
	require.NoError(t, err)
	require.NotNil(t, cli)

	packCli, err := packCliPath()
	require.NoError(t, err)

	contents, err := os.ReadFile(packCli)
	require.NoError(t, err)

	require.Equal(t, "pack cli", string(contents))
}

func TestNewPackCliUpgrade(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	cliPath, err := packCliPath()
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Dir(cliPath), 0700)
	require.NoError(t, err)

	err = os.WriteFile(cliPath, []byte("old pack cli"), 0600)
	require.NoError(t, err)

	mockContext := mocks.NewMockContext(context.Background())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Host == "github.com"
	}).Respond(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("pack cli")),
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.HasSuffix(args.Cmd, packName()) && args.Args[0] == "--version" && len(args.Args) == 1
	}).Respond(exec.NewRunResult(
		0,
		fmt.Sprintf("%s+git-c38f7da.build-4952", semver.MustParse("0.0.1").String()),
		"",
	))

	mockExtract := func(src, dst string) (string, error) {
		exp, _ := packCliPath()
		_ = osutil.Rename(context.Background(), src, exp)
		return src, nil
	}

	cli, err := newPackCliImpl(
		*mockContext.Context,
		mockContext.Console,
		mockContext.CommandRunner,
		mockContext.HttpClient,
		mockExtract,
	)
	require.NoError(t, err)
	require.NotNil(t, cli)

	packCli, err := packCliPath()
	require.NoError(t, err)

	contents, err := os.ReadFile(packCli)
	require.NoError(t, err)

	require.Equal(t, "pack cli", string(contents))
}
