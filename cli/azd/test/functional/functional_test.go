// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package functional

import (
	"bytes"
	"flag"
	"log"
	"os"

	osexec "os/exec"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// test for azd deploy, azd deploy --service
func Test_CLI_DeployInvalidName(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "eastus2")

	ctx, cancel := NewTestContext(t)
	defer cancel()

	dir := TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)

	err := CopySample(dir, "webapp")
	require.NoError(t, err, "failed expanding sample")

	_, initResp := NewRandomNameEnvAndInitResponse(t)

	_, _, err = RunCliCommandWithStdIn(t, ctx, initResp, "init")
	require.NoError(t, err)

	_, _, err = RunCliCommand(t, ctx, "deploy", "--service", "badServiceName")
	require.ErrorContains(t, err, "'badServiceName' doesn't exist")
}

func Test_CLI_ProjectIsNeeded(t *testing.T) {
	ctx, cancel := NewTestContext(t)
	defer cancel()

	dir := TempDirWithDiagnostics(t)

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
		t.Run(test.command, func(t *testing.T) {
			ostest.Chdir(t, dir)

			args := strings.Split(test.command, " ")
			if test.args != nil {
				args = append(args, test.args...)
			}

			_, stderr, err := RunCliCommand(t, ctx, args...)

			assert.Error(t, err)
			assert.Regexp(t, "no project exists; to create a new project, run `azd init`", stderr)
		})
	}
}

func Test_CLI_NoDebugSpewWhenHelpPassedWithoutDebug(t *testing.T) {
	stdErrBuf := bytes.Buffer{}

	cmd := osexec.Command(getAzdLocation(), "--help")
	cmd.Stderr = &stdErrBuf

	// Run the command and wait for it to complete, we don't expect any errors.
	err := cmd.Start()
	assert.NoError(t, err)

	err = cmd.Wait()
	assert.NoError(t, err)

	// Ensure no output was written to stderr
	assert.Equal(t, "", stdErrBuf.String(), "no output should be written to stderr when --help is passed")
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
