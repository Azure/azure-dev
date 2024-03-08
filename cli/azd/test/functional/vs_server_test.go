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
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/azure/azure-dev/cli/azd/test/recording"
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

	type vsServerTest struct {
		Name string
		// The test may use live resources requiring cleanup.
		IsLive bool
	}
	var tests []vsServerTest
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "The following Tests are available:") {
			testStart = true
			continue
		}

		if testStart && strings.HasPrefix(line, "    ") {
			test := vsServerTest{}
			name := strings.TrimSpace(line)

			test.Name = name
			if strings.HasPrefix(name, "Live") {
				test.IsLive = true
			}
			tests = append(tests, test)
		}
	}

	stdout.Reset()
	stderr.Reset()
	cmd = exec.CommandContext(context.Background(), "dotnet", "build")
	cmd.Dir = testDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	require.NoError(t, err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())

	// For each test, copySample
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			tt := tt
			t.Parallel()

			ctx, cancel := newTestContext(t)
			defer cancel()

			dir := tempDirWithDiagnostics(t)
			t.Logf("DIR: %s", dir)

			err = copySample(dir, "aspire-full")
			require.NoError(t, err, "failed expanding sample")

			var session *recording.Session
			envName := ""
			subscriptionId := cfg.SubscriptionID

			if tt.IsLive {
				session = recording.Start(t)
				envName = randomOrStoredEnvName(session)
				subscriptionId = cfgOrStoredSubscription(session)
			}

			cli := azdcli.NewCLI(t, azdcli.WithSession(session))
			/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments false positive */
			cmd := exec.CommandContext(ctx, cli.AzdPath, "vs-server")
			cmd.Env = append(cli.Env, os.Environ()...)
			cmd.Env = append(cmd.Env, "AZD_DEBUG_SERVER_DEBUG_ENDPOINTS=true")
			pathString := ostest.CombinedPaths(cmd.Env)
			if len(pathString) > 0 {
				cmd.Env = append(cmd.Env, pathString)
			}

			var stdout bytes.Buffer
			cmd.Stdout = io.MultiWriter(&stdout, &logWriter{initialTime: time.Now(), t: t, prefix: "[svr-out] "})
			cmd.Stderr = &logWriter{initialTime: time.Now(), t: t, prefix: "[svr-err] "}
			err = cmd.Start()
			require.NoError(t, err)

			// Wait for the server to start
			for i := 0; i < 5; i++ {
				time.Sleep(300 * time.Millisecond)
				if stdout.Len() > 0 {
					break
				}
			}

			var svr contracts.VsServerResult
			err = json.Unmarshal(stdout.Bytes(), &svr)
			require.NoError(t, err, "value: %s", stdout.String())

			/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments false positive */
			cmd = exec.CommandContext(context.Background(),
				"dotnet", "test",
				"--no-build",
				"--no-restore",
				"--filter", "Name="+tt.Name)
			cmd.Dir = testDir
			cmd.Env = append(cmd.Env, os.Environ()...)
			cmd.Env = append(cmd.Env, "AZURE_SUBSCRIPTION_ID="+subscriptionId)
			cmd.Env = append(cmd.Env, "AZURE_LOCATION="+cfg.Location)
			cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", svr.Port))
			cmd.Env = append(cmd.Env, "ROOT_DIR="+dir)
			if tt.IsLive {
				cmd.Env = append(cmd.Env, "AZURE_ENV_NAME="+envName)
			}

			cmd.Stdout = &logWriter{initialTime: time.Now(), t: t, prefix: "[t-out] "}
			cmd.Stderr = &logWriter{initialTime: time.Now(), t: t, prefix: "[t-err] "}
			err = cmd.Run()
			require.NoError(t, err)

			if tt.IsLive {
				// We don't currently have a way to deprovision using server mode.
				// For now let's just clean up the resources.
				cli.WorkingDirectory = dir
				cli.Env = append(cli.Env, os.Environ()...)
				_, _ = cli.RunCommand(ctx, "down", "--force", "--purge")
			}
		})
	}
}
