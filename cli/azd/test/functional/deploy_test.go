package cli_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/google/go-cmdtest"
	"github.com/stretchr/testify/require"
)

func shouldUpdate() bool {
	val, ok := os.LookupEnv("UPDATE")
	if !ok {
		return false
	}

	update, err := strconv.ParseBool(val)
	if err != nil {
		fmt.Printf("env var UPDATE is set to an invalid value: %s\n", val)
		panic(err)
	}
	return update
}

func hideCmd(commands map[string]cmdtest.CommandFunc) cmdtest.CommandFunc {
	return func(args []string, inputFile string) ([]byte, error) {
		return hideCmdImpl(args, inputFile, commands)
	}
}

// Hides command output. Useful for testing commands that output to stdout in a non-deterministic fashion (spinners).
func hideCmdImpl(
	args []string,
	inputFile string,
	commands map[string]cmdtest.CommandFunc) ([]byte, error) {
	if len(args) < 1 {
		return nil, errors.New("need at least 1 argument")
	}
	if inputFile != "" {
		return nil, errors.New("input redirection not supported")
	}
	if args[0] == "hide" {
		return nil, errors.New("cannot hide the hide command")
	}

	cmd, ok := commands[args[0]]
	if !ok {
		return nil, fmt.Errorf("invalid cmd: %s", args[0]))
	}

	// discard output
	_, err := cmd(args[1:], "")
	return nil, err
}

func Test_Deploy_Errors(t *testing.T) {
	t.Setenv("TERM", "dumb")

	ts, err := cmdtest.Read(filepath.Join("testdata", "cmd", "working-directory"))
	require.NoError(t, err)

	cli := azdcli.NewCLI(t)
	ts.Commands["hide"] = hideCmd(ts.Commands)
	ts.Commands["azd"] = cmdtest.Program(cli.AzdPath)
	ts.Setup = func(path string) error {
		return copySample(path, "webapp")
	}
	ts.Run(t, shouldUpdate())
}
