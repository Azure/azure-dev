// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

// Package cli drives the built binary as a subprocess.
//
// The other live tests call the client layer directly, which proves the API
// paths work but says nothing about the command surface on top of them: flag
// parsing, mutual exclusion, prompting, --no-prompt, exit codes, the rendered
// tables, and whether -o json emits what a script can actually consume. Those
// are the parts a user touches, and until now nothing exercised them against a
// real service.
//
//	go test -tags live -v ./tests/cli/...
//
// Required:
//
//	AZURE_AI_EVAL_E2E_LIVE=1
//	FOUNDRY_PROJECT_ENDPOINT=https://<account>.services.ai.azure.com/api/projects/<project>
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	binaryPath string
	endpoint   string
)

// TestMain builds the extension once so every test runs the same binary a user
// would, rather than an in-process command tree that skips main's wiring.
func TestMain(m *testing.M) {
	if os.Getenv("AZURE_AI_EVAL_E2E_LIVE") != "1" {
		fmt.Fprintln(os.Stderr, "set AZURE_AI_EVAL_E2E_LIVE=1 to run the CLI tests")
		os.Exit(0)
	}

	endpoint = strings.TrimSuffix(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"), "/")
	if endpoint == "" {
		fmt.Fprintln(os.Stderr, "FOUNDRY_PROJECT_ENDPOINT is required")
		os.Exit(1)
	}

	dir, err := os.MkdirTemp("", "azdeval-cli")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating a temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	binaryPath = filepath.Join(dir, "azdeval"+exeSuffix())
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the extension: %v\n%s\n", err, out)
		os.Exit(1)
	}

	code := m.Run()
	// The shared eval outlives any single test, so it cannot be released with
	// t.Cleanup without taking it away from the tests that run after.
	runTeardown()
	os.RemoveAll(dir)
	os.Exit(code)
}

func exeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

// result is one invocation of the binary.
type result struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
}

// Combined is stdout and stderr together, for assertions that do not care
// which stream carried the message.
func (r result) Combined() string { return r.Stdout + r.Stderr }

// JSON decodes stdout, failing the test when the command did not emit
// something a script could consume.
func (r result) JSON(t *testing.T, into any) {
	t.Helper()
	require.NoError(t, json.Unmarshal([]byte(r.Stdout), into),
		"-o json must emit parseable JSON on stdout; got:\n%s", r.Stdout)
}

// run invokes the binary with the project endpoint already supplied.
func run(t *testing.T, args ...string) result {
	t.Helper()
	return runIn(t, "", args...)
}

// credentialFlake is azd's token helper failing under rapid sequential calls.
//
// Every invocation here is a fresh process, so each one shells out to azd for
// a token, and azd intermittently exits non-zero doing it. Retrying is safe
// because no request was made, and the alternative is a suite that fails on a
// different test each run for a reason that has nothing to do with the code.
const credentialFlake = "AzureDeveloperCLICredential: exit status 1"

// runIn invokes the binary with a working directory, for commands that write
// files.
func runIn(t *testing.T, dir string, args ...string) result {
	t.Helper()

	res := invoke(t, dir, args...)
	for attempt := 0; attempt < 2 && strings.Contains(res.Combined(), credentialFlake); attempt++ {
		t.Logf("azd credential flaked; retrying `%s`", strings.Join(args, " "))
		time.Sleep(2 * time.Second)
		res = invoke(t, dir, args...)
	}
	require.NotContains(t, res.Combined(), credentialFlake,
		"azd could not produce a token after retries; run `azd auth login` and try again")
	return res
}

func invoke(t *testing.T, dir string, args ...string) result {
	t.Helper()

	full := append([]string{}, args...)
	if !hasFlag(args, "--project-endpoint") && needsEndpoint(args) {
		full = append(full, "--project-endpoint", endpoint)
	}

	cmd := exec.Command(binaryPath, full...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("could not run %v: %v", full, err)
	}

	res := result{Args: full, Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
	t.Logf("$ azd ai eval %s -> exit %d", strings.Join(args, " "), res.ExitCode)
	return res
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// needsEndpoint keeps --project-endpoint off the commands that reject it.
func needsEndpoint(args []string) bool {
	for _, a := range args {
		switch a {
		case "init", "--help", "-h":
			return false
		}
	}
	return true
}

// requireSuccess fails with the command's own output, which is what a user
// would have seen.
func requireSuccess(t *testing.T, r result) result {
	t.Helper()
	require.Equalf(t, 0, r.ExitCode,
		"expected `%s` to succeed\nstdout:\n%s\nstderr:\n%s",
		strings.Join(r.Args, " "), r.Stdout, r.Stderr)
	return r
}

// requireFailure asserts a non-zero exit, so a command that silently succeeds
// where it should refuse is caught.
func requireFailure(t *testing.T, r result) result {
	t.Helper()
	require.NotEqualf(t, 0, r.ExitCode,
		"expected `%s` to fail\nstdout:\n%s\nstderr:\n%s",
		strings.Join(r.Args, " "), r.Stdout, r.Stderr)
	return r
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
