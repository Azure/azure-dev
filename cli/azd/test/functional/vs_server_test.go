// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

func Test_CLI_VsServer(t *testing.T) {
	testDir := filepath.Join("testdata", "vs-server", "tests")
	// List all tests
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(context.Background(), "dotnet", "test", "--list-tests")
	cmd.Dir = testDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoError(t, err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())

	scanner := bufio.NewScanner(&stdout)
	testStart := false
	var tests []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "The following Tests are available:") {
			testStart = true
			continue
		}

		if testStart && strings.HasPrefix(line, "    ") {
			tests = append(tests, strings.TrimSpace(line))
		}
	}

	// For each test, copySample
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			ctx, cancel := newTestContext(t)
			defer cancel()

			dir := tempDirWithDiagnostics(t)
			t.Logf("DIR: %s", dir)

			err = copySample(dir, "aspire-full")
			require.NoError(t, err, "failed expanding sample")

			cli := azdcli.NewCLI(t)
			cmd := exec.CommandContext(ctx, cli.AzdPath, "vs-server", "--debug")
			cmd.Env = append(cmd.Env, os.Environ()...)
			cmd.Env = append(cmd.Env, "AZD_DEBUG_SERVER_DEBUG_ENDPOINTS=true")

			stdout.Reset()
			cmd.Stdout = io.MultiWriter(&stdout, &logWriter{initialTime: time.Now(), t: t, prefix: "[svr-out] "})
			cmd.Stderr = &logWriter{initialTime: time.Now(), t: t, prefix: "[svr-err] "}
			err = cmd.Start()
			require.NoError(t, err)

			// Wait for the server to start
			time.Sleep(300 * time.Millisecond)

			var svr contracts.VsServerResult
			err = json.Unmarshal(stdout.Bytes(), &svr)
			require.NoError(t, err, "value: %s", stdout.String())

			cmd = exec.CommandContext(context.Background(),
				"dotnet", "test",
				"--filter", "Name="+tt)
			cmd.Dir = testDir
			cmd.Env = append(cmd.Env, os.Environ()...)
			cmd.Env = append(cmd.Env, "AZURE_SUBSCRIPTION_ID="+"faa080af-c1d8-40ad-9cce-e1a450ca5b57")
			cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", svr.Port))
			cmd.Env = append(cmd.Env, "ROOT_DIR="+dir)

			cmd.Stdout = &logWriter{initialTime: time.Now(), t: t, prefix: "[t-out] "}
			cmd.Stderr = &logWriter{initialTime: time.Now(), t: t, prefix: "[t-err] "}
			err = cmd.Run()
			require.NoError(t, err)
		})
	}
}
