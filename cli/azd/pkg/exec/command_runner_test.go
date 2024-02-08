package exec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ParensInCommand(t *testing.T) {
	cmdPath := getCmdPath()

	root := filepath.Join(t.TempDir(), "johndoe(somecompany)")
	require.NoError(t, os.MkdirAll(root, 0755))

	helperBinaryName := "argprint"
	if runtime.GOOS == "windows" {
		helperBinaryName = "argprint.exe"
	}

	err := build(cmdPath, "-o", filepath.Join(root, helperBinaryName))
	require.NoError(t, err)

	runner := NewCommandRunner(nil)
	args := NewRunArgs(filepath.Join(root, helperBinaryName), "arg1", "arg2 with spaces", "arg3 (with parens)")
	res, err := runner.Run(context.Background(), args)
	require.NoError(t, err)

	// The test program just prints each entry of os.Args on its own line. Validate we got that.
	expected := fmt.Sprintf("%s\n%s\n", args.Cmd, strings.Join(args.Args, "\n"))

	require.Equal(t, expected, res.Stdout)
}

func getCmdPath() string {
	_, b, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(b), "testdata", "argprint")
}

func build(pkgPath string, args ...string) error {
	cmd := exec.Command("go", "build")
	cmd.Dir = pkgPath
	cmd.Args = append(cmd.Args, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"failed to build %s in %s: %w:\n%s",
			strings.Join(cmd.Args, " "),
			cmd.Dir,
			err,
			output)
	}

	return nil
}
