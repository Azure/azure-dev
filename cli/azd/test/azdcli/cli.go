// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

// Contains support for automating the use of the azd CLI

package azdcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/azure/azure-dev/cli/azd/test/recording"
)

const (
	HeartbeatInterval = 10 * time.Second
)

// sync.Once for one-time build for the process invocation
var buildOnce sync.Once

// sync.Once for one-time build for the process invocation (record mode)
var buildRecordOnce sync.Once

// The result of calling an azd CLI command
type CliResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// The azd CLI.
//
// Consumers should use the NewCLI constructor to initialize this struct.
type CLI struct {
	T                *testing.T
	WorkingDirectory string
	Env              []string
	// Path to the azd binary
	AzdPath string
}

// Constructs the CLI.
// On a local developer machine, this also ensures that the azd binary is up-to-date before running.
//
// By default, the path to the default source location is used, see GetSourcePath.
// Environment variable CLI_TEST_AZD_PATH can be used to set the path to the azd binary. This can be done in CI to
// run the tests against a specific azd binary.
//
// When CI is detected, no automatic build is performed. To disable automatic build behavior, CLI_TEST_SKIP_BUILD
// can be set to a truthy value.
func NewCLI(t *testing.T, opts ...Options) *CLI {
	cli := &CLI{T: t}
	opt := option{}
	for _, o := range opts {
		o.Apply(&opt)
	}

	if opt.Session != nil {
		env := append(
			environ(opt.Session),
			"AZD_TEST_HTTPS_PROXY="+opt.Session.ProxyUrl,
			"AZD_DEBUG_PROVISION_PROGRESS_DISABLE=true",
			"PATH="+strings.Join(opt.Session.CmdProxyPaths, string(os.PathListSeparator)))
		cli.Env = append(cli.Env, env...)

		if opt.Session.Playback {
			if subId, has := opt.Session.Variables[recording.SubscriptionIdKey]; has {
				cli.Env = append(cli.Env, fmt.Sprintf("AZD_DEBUG_SYNTHETIC_SUBSCRIPTION=%s", subId))
			}
		}
	}

	// Allow a override for custom build
	if os.Getenv("CLI_TEST_AZD_PATH") != "" {
		cli.AzdPath = os.Getenv("CLI_TEST_AZD_PATH")
		return cli
	}

	// Set AzdPath to the appropriate binary path
	sourceDir := GetSourcePath()
	name := "azd"
	if opt.Session != nil {
		name = "azd-record"
	}
	if runtime.GOOS == "windows" {
		name = name + ".exe"
	}
	cli.AzdPath = filepath.Join(sourceDir, name)

	// Manual override for skipping automatic build
	skip, err := strconv.ParseBool(os.Getenv("CLI_TEST_SKIP_BUILD"))
	if err == nil && skip {
		return cli
	}

	// Skip automatic build in CI always
	if os.Getenv("CI") != "" ||
		strings.ToLower(os.Getenv("TF_BUILD")) == "true" ||
		strings.ToLower(os.Getenv("GITHUB_ACTIONS")) == "true" {
		return cli
	}

	if opt.Session != nil {
		buildRecordOnce.Do(func() {
			build(t, sourceDir, "-tags=record", "-o="+name)
		})
	} else {
		buildOnce.Do(func() {
			build(t, sourceDir)
		})
	}

	return cli
}

func (cli *CLI) RunCommandWithStdIn(ctx context.Context, stdin string, args ...string) (*CliResult, error) {
	description := "azd " + strings.Join(args, " ") + " in " + cli.WorkingDirectory

	/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments false positive */
	cmd := osexec.CommandContext(ctx, cli.AzdPath, args...)
	if cli.WorkingDirectory != "" {
		cmd.Dir = cli.WorkingDirectory
	}

	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = os.Stdin
	}

	cmd.Env = cli.Env

	// Collect all PATH variables, appending in the order it was added, to form a single PATH variable
	pathString := ostest.CombinedPaths(cmd.Env)

	if len(pathString) > 0 {
		cmd.Env = append(cmd.Env, pathString)
	}

	// we run a background goroutine to report a heartbeat in the logs while the command
	// is still running. This makes it easy to see what's still in progress if we hit a timeout.
	done := make(chan struct{})
	go func() {
		cli.heartbeat(description, done)
	}()
	defer func() {
		done <- struct{}{}
	}()

	now := time.Now()
	stdOutLogger := &logWriter{t: cli.T, prefix: "[stdout] ", initialTime: now}
	stdErrLogger := &logWriter{t: cli.T, prefix: "[stderr] ", initialTime: now}

	var stderr, stdout bytes.Buffer
	cmd.Stderr = io.MultiWriter(&stderr, stdErrLogger)
	cmd.Stdout = io.MultiWriter(&stdout, stdOutLogger)
	err := cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start command '%s': %w", description, err)
	}

	err = cmd.Wait()
	result := &CliResult{
		ExitCode: cmd.ProcessState.ExitCode(),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("command '%s' timed out: %w", description, err)
	}

	if errors.Is(ctx.Err(), context.Canceled) {
		// bubble up errors due to cancellation with their output, and let the caller
		// decide how to handle it.
		return result, ctx.Err()
	}

	if err != nil {
		return result, fmt.Errorf("command '%s' had non-zero exit code: %w", description, err)
	}

	return result, nil
}

func (cli *CLI) RunCommand(ctx context.Context, args ...string) (*CliResult, error) {
	return cli.RunCommandWithStdIn(ctx, "", args...)
}

func (cli *CLI) heartbeat(description string, done <-chan struct{}) {
	start := time.Now()
	for {
		select {
		case <-time.After(HeartbeatInterval):
			cli.T.Logf("[heartbeat] command %s is still running after %s",
				description, time.Since(start).Truncate(time.Second))
		case <-done:
			return
		}
	}
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

		output := exec.RedactSensitiveData(l.sb.String())

		if b == '\n' {
			l.t.Logf("%s %s%s", time.Since(l.initialTime).Round(1*time.Millisecond), l.prefix, output)
			l.sb.Reset()
		}
	}

	return len(bytes), nil
}

func GetSourcePath() string {
	_, b, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(b), "..", "..")
}

func build(t *testing.T, pkgPath string, args ...string) {
	startTime := time.Now()
	cmd := osexec.Command("go", "build")
	cmd.Dir = pkgPath
	cmd.Args = append(cmd.Args, args...)

	// Build with coverage if GOCOVERDIR is specified.
	if os.Getenv("GOCOVERDIR") != "" {
		cmd.Args = append(cmd.Args, "-cover")
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		panic(fmt.Errorf(
			"failed to build azd (ran %s in %s): %w:\n%s",
			strings.Join(cmd.Args, " "),
			cmd.Dir,
			err,
			output))
	}

	t.Logf("built azd in %s (%s)", time.Since(startTime), strings.Join(cmd.Args, " "))
}

// Recording variables that are mapped to environment variables.
var recordingVarToEnvVar = map[string]string{
	// Fixed time for the CLI. See deps_record.go
	recording.TimeKey: "AZD_TEST_FIXED_CLOCK_UNIX_TIME",
	// Set the default subscription used in the test
	recording.SubscriptionIdKey: "AZURE_SUBSCRIPTION_ID",
}

func environ(session *recording.Session) []string {
	if session == nil {
		return nil
	}

	env := []string{}
	for recordKey, envKey := range recordingVarToEnvVar {
		if _, ok := session.Variables[recordKey]; ok {
			env = append(env, fmt.Sprintf("%s=%s", envKey, session.Variables[recordKey]))
		}
	}
	return env
}

// TestCredential Used to used the auth strategy already used to create the Cli instance
type TestCredential struct {
	cli *CLI
}

// NewTestCredential Creates a new TestCredential
func NewTestCredential(azCli *CLI) *TestCredential {
	return &TestCredential{
		cli: azCli,
	}
}

// GetToken Gets the token from the CLI instance
func (cred *TestCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	result, err := cred.cli.RunCommand(ctx, "auth", "token", "-o", "json")
	if err != nil {
		return azcore.AccessToken{}, err
	}

	var accessToken azcore.AccessToken
	if err := json.Unmarshal([]byte(result.Stdout), &accessToken); err != nil {
		return azcore.AccessToken{}, err
	}

	return accessToken, nil
}
