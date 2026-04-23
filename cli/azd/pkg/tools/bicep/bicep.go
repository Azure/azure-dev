// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

// Version is the minimum version of bicep that we require (and the one we fetch when we fetch bicep on behalf of a
// user).
var Version semver.Version = semver.MustParse("0.42.1")

// Cli is a wrapper around the bicep CLI.
// The CLI automatically ensures bicep is installed before executing commands.
//
// Concurrency notes: The sync.Mutex is per-instance, not global. In normal operation, the IoC
// container registers Cli as a singleton, so one shared instance is used. However, some code paths
// (e.g., service_target_containerapp.go) create new instances inline. If multiple instances race to
// install bicep concurrently, this is safe because downloadBicep uses atomic file operations
// (temp file + rename). The only downside is potentially redundant downloads, which is rare and harmless.
type Cli struct {
	path        string
	runner      exec.CommandRunner
	console     input.Console
	transporter policy.Transporter

	// buildCache stores compiled BuildResult values keyed by content hash to avoid redundant
	// recompilation within the same azd process.
	buildCache sync.Map

	installInit osutil.LazyRetryInit
}

// NewCli creates a new Bicep CLI wrapper.
// The CLI automatically ensures bicep is installed when Build or BuildBicepParam is called.
func NewCli(console input.Console, commandRunner exec.CommandRunner) *Cli {
	return newCliWithTransporter(console, commandRunner, http.DefaultClient)
}

// newCliWithTransporter is like NewCli but allows providing a custom transport for testing.
func newCliWithTransporter(
	console input.Console,
	commandRunner exec.CommandRunner,
	transporter policy.Transporter,
) *Cli {
	return &Cli{
		runner:      commandRunner,
		console:     console,
		transporter: transporter,
	}
}

// ensureInstalledOnce checks if bicep is available and downloads/upgrades if needed.
// This is safe to call multiple times; successful installation is cached and failed attempts are retried.
func (cli *Cli) ensureInstalledOnce(ctx context.Context) error {
	return cli.installInit.Do(func() error {
		return cli.ensureInstalled(ctx)
	})
}

func (cli *Cli) ensureInstalled(ctx context.Context) error {
	if override := os.Getenv("AZD_BICEP_TOOL_PATH"); override != "" {
		//nolint:gosec // G706: env var in debug log
		log.Printf("using external bicep tool: %s", override)
		cli.path = override
		return nil
	}

	bicepPath, err := azdBicepPath()
	if err != nil {
		return fmt.Errorf("finding bicep: %w", err)
	}

	if _, err = os.Stat(bicepPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("finding bicep: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(bicepPath), osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("downloading bicep: %w", err)
		}

		if err := runStep(
			ctx, cli.console, "Downloading Bicep", func() error {
				return downloadBicep(ctx, cli.transporter, Version, bicepPath)
			},
		); err != nil {
			return fmt.Errorf("downloading bicep: %w", err)
		}
	}

	cli.path = bicepPath

	ver, err := cli.version(ctx)
	if err != nil {
		return fmt.Errorf("checking bicep version: %w", err)
	}

	log.Printf("bicep version: %s", ver)

	if ver.LT(Version) {
		log.Printf("installed bicep version %s is older than %s; updating.", ver.String(), Version.String())

		if err := runStep(
			ctx, cli.console, "Upgrading Bicep", func() error {
				return downloadBicep(ctx, cli.transporter, Version, bicepPath)
			},
		); err != nil {
			return fmt.Errorf("upgrading bicep: %w", err)
		}
	}

	log.Printf("using local bicep: %s", bicepPath)

	return nil
}

// runStep runs a long running operation, using the console to show a spinner for progress and status.
func runStep(ctx context.Context, console input.Console, title string, action func() error) error {
	console.ShowSpinner(ctx, title, input.Step)
	err := action()

	if err != nil {
		console.StopSpinner(ctx, title, input.StepFailed)
		return err
	}

	console.StopSpinner(ctx, title, input.StepDone)
	return nil
}

// azdBicepPath returns the path where we store our local copy of bicep ($AZD_CONFIG_DIR/bin).
func azdBicepPath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		return filepath.Join(configDir, "bin", "bicep.exe"), nil
	}

	return filepath.Join(configDir, "bin", "bicep"), nil
}

// downloadBicep downloads a given version of bicep from the release site, writing the output to name.
func downloadBicep(ctx context.Context, transporter policy.Transporter, bicepVersion semver.Version, name string) error {
	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}

	var releaseName string
	switch runtime.GOOS {
	case "windows":
		releaseName = fmt.Sprintf("bicep-win-%s.exe", arch)
	case "darwin":
		releaseName = fmt.Sprintf("bicep-osx-%s", arch)
	case "linux":
		if preferMuslBicep(os.Stat) {
			if runtime.GOARCH != "arm64" {
				return fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
			}

			releaseName = "bicep-linux-musl-x64"
		} else {
			releaseName = fmt.Sprintf("bicep-linux-%s", arch)
		}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	bicepReleaseUrl := fmt.Sprintf("https://downloads.bicep.azure.com/v%s/%s", bicepVersion, releaseName)

	log.Printf("downloading bicep release %s -> %s", bicepReleaseUrl, name)

	req, err := http.NewRequestWithContext(ctx, "GET", bicepReleaseUrl, nil)
	if err != nil {
		return err
	}

	resp, err := transporter.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http error %d", resp.StatusCode)
	}

	f, err := os.CreateTemp(filepath.Dir(name), fmt.Sprintf("%s.tmp*", filepath.Base(name)))
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name()) //nolint:gosec // G703: temp file cleanup
	}()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}

	if err := f.Chmod(osutil.PermissionExecutableFile); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := osutil.Rename(ctx, f.Name(), name); err != nil {
		return err
	}

	return nil
}

type stater func(name string) (os.FileInfo, error)

// preferMuslBicep determines if we should install the version of bicep that used musl instead of glibc. We prefer
// musl bicep on linux systems that have musl installed and do not have glibc installed. If both musl and glibc are
// installed, we prefer the glibc based version.  This behavior matches the `az` CLI (see: Azure/azure-cli#23040)
func preferMuslBicep(stat stater) bool {
	if _, err := stat("/lib/ld-musl-x86_64.so.1"); err == nil {
		if _, err := stat("/lib/x86_64-linux-gnu/libc.so.6"); err == nil {
			return false
		}

		return true
	}

	return false
}

func (cli *Cli) version(ctx context.Context) (semver.Version, error) {
	bicepRes, err := cli.runCommand(ctx, nil, "--version")
	if err != nil {
		return semver.Version{}, err
	}

	bicepSemver, err := tools.ExtractVersion(bicepRes.Stdout)
	if err != nil {
		return semver.Version{}, err
	}

	return bicepSemver, nil

}

type BuildResult struct {
	// The compiled ARM template
	Compiled string

	// Lint error message, if any
	LintErr string
}

// modulePattern matches Bicep module declarations that reference local files.
// It captures the relative path from: module <name> '<path>' ...
var modulePattern = regexp.MustCompile(`(?m)module\s+\w+\s+'([^']+)'`)

// buildCacheKey computes a SHA-256 digest over the Bicep file's content, its matching
// .bicepparam file (when present), and all recursively referenced local module files.
// Registry modules (br: and ts: prefixes) are ignored since they are externally versioned.
// Returns a non-nil error when any referenced module file cannot be read; the caller
// (Build) treats any such error as a cache miss and falls through to a live build.
func (cli *Cli) buildCacheKey(file string) (string, error) {
	h := sha256.New()
	visited := make(map[string]bool)

	if err := hashBicepFileTree(file, h, visited, 0); err != nil {
		return "", err
	}

	// Also incorporate the companion .bicepparam file when it exists so that
	// parameter-only changes correctly invalidate the cache.
	paramFile := strings.TrimSuffix(file, filepath.Ext(file)) + ".bicepparam"
	if paramContent, err := os.ReadFile(paramFile); err == nil {
		h.Write(paramContent)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashBicepFileTree reads a Bicep file, writes its content to the hash, and recursively
// processes any local module imports found in the file. visited tracks already-processed
// absolute paths to avoid cycles. depth limits recursion to prevent stack overflow from
// deeply nested or pathological module references. Returns a non-nil error if the file
// (or any referenced module) cannot be read — the caller should treat this as a cache miss.
func hashBicepFileTree(file string, h io.Writer, visited map[string]bool, depth int) error {
	if depth > 100 {
		return fmt.Errorf("hashBicepFileTree: recursion depth exceeded 100 at %s", file)
	}

	absPath, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	if visited[absPath] {
		return nil
	}
	visited[absPath] = true

	// absPath originates from developer-controlled inputs: the initial Bicep
	// template path configured in the azd project, plus module paths embedded
	// in those Bicep files. This function is read-only and only feeds the
	// content into a SHA-256 hash used as an in-memory build-cache key. There
	// is no untrusted-user input in this flow, so path traversal via taint is
	// not a real risk here.
	//nolint:gosec // G304/G703: developer-controlled bicep paths, read-only cache hashing.
	content, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	if _, err := h.Write(content); err != nil {
		return err
	}

	// Scan for module declarations referencing local files.
	dir := filepath.Dir(absPath)
	for _, match := range modulePattern.FindAllSubmatch(content, -1) {
		modulePath := string(match[1])

		// Skip registry modules (br: for Bicep Registry, ts: for Template Specs).
		if strings.HasPrefix(modulePath, "br:") || strings.HasPrefix(modulePath, "ts:") {
			continue
		}

		resolved := filepath.Join(dir, modulePath)

		// Validate that the resolved module path stays within the project
		// directory tree (the directory containing the root Bicep file).
		if rel, relErr := filepath.Rel(dir, resolved); relErr == nil {
			if strings.HasPrefix(rel, "..") {
				log.Printf("warning: bicep module path %q resolves outside project directory, skipping", modulePath)
				continue
			}
		}

		if err := hashBicepFileTree(resolved, h, visited, depth+1); err != nil {
			// Module file unresolvable — force cache miss.
			return err
		}
	}

	return nil
}

func (cli *Cli) Build(ctx context.Context, file string) (BuildResult, error) {
	// Check in-memory cache.
	if key, err := cli.buildCacheKey(file); err == nil {
		if cached, ok := cli.buildCache.Load(key); ok {
			log.Printf("bicep build cache hit for %s", file)
			return cached.(BuildResult), nil
		}
	}

	if err := cli.ensureInstalledOnce(ctx); err != nil {
		return BuildResult{}, fmt.Errorf("ensuring bicep is installed: %w", err)
	}

	args := []string{"build", file, "--stdout"}
	buildRes, err := cli.runCommand(ctx, nil, args...)

	if err != nil {
		return BuildResult{}, fmt.Errorf(
			"failed running bicep build: %w",
			err,
		)
	}

	result := BuildResult{
		Compiled: buildRes.Stdout,
		LintErr:  buildRes.Stderr,
	}

	// Store in cache for subsequent calls within the same process.
	if key, err := cli.buildCacheKey(file); err == nil {
		cli.buildCache.Store(key, result)
		log.Printf("bicep build cache store for %s", file)
	}

	return result, nil
}

func (cli *Cli) BuildBicepParam(ctx context.Context, file string, env []string) (BuildResult, error) {
	if err := cli.ensureInstalledOnce(ctx); err != nil {
		return BuildResult{}, fmt.Errorf("ensuring bicep is installed: %w", err)
	}

	args := []string{"build-params", file, "--stdout"}
	buildRes, err := cli.runCommand(ctx, env, args...)

	if err != nil {
		return BuildResult{}, fmt.Errorf(
			"failed running bicep build: %w",
			err,
		)
	}

	return BuildResult{
		Compiled: buildRes.Stdout,
		LintErr:  buildRes.Stderr,
	}, nil
}

// SnapshotOptions configures optional flags for the `bicep snapshot` command.
// Use the With* methods to set values using the builder pattern:
//
//	opts := NewSnapshotOptions().
//	    WithMode("validate").
//	    WithSubscriptionID("sub-123").
//	    WithLocation("eastus2")
type SnapshotOptions struct {
	// Mode sets the snapshot mode: "overwrite" (generate new) or "validate" (compare against existing).
	Mode string
	// TenantID is the tenant ID to use for the deployment.
	TenantID string
	// SubscriptionID is the subscription ID to use for the deployment.
	SubscriptionID string
	// ResourceGroup is the resource group name to use for the deployment.
	ResourceGroup string
	// Location is the location to use for the deployment.
	Location string
	// DeploymentName is the deployment name to use.
	DeploymentName string
}

// NewSnapshotOptions returns a zero-valued SnapshotOptions ready for building.
func NewSnapshotOptions() SnapshotOptions {
	return SnapshotOptions{}
}

// WithMode sets the snapshot mode ("overwrite" or "validate").
func (o SnapshotOptions) WithMode(mode string) SnapshotOptions {
	o.Mode = mode
	return o
}

// WithTenantID sets the tenant ID for the deployment.
func (o SnapshotOptions) WithTenantID(tenantID string) SnapshotOptions {
	o.TenantID = tenantID
	return o
}

// WithSubscriptionID sets the subscription ID for the deployment.
func (o SnapshotOptions) WithSubscriptionID(subscriptionID string) SnapshotOptions {
	o.SubscriptionID = subscriptionID
	return o
}

// WithResourceGroup sets the resource group name for the deployment.
func (o SnapshotOptions) WithResourceGroup(resourceGroup string) SnapshotOptions {
	o.ResourceGroup = resourceGroup
	return o
}

// WithLocation sets the location for the deployment.
func (o SnapshotOptions) WithLocation(location string) SnapshotOptions {
	o.Location = location
	return o
}

// WithDeploymentName sets the deployment name.
func (o SnapshotOptions) WithDeploymentName(deploymentName string) SnapshotOptions {
	o.DeploymentName = deploymentName
	return o
}

// Snapshot runs `bicep snapshot <file>` and reads the resulting snapshot file.
// The bicep CLI produces a `<basename>.snapshot.json` file next to the input .bicepparam file.
// This method reads the snapshot content into a byte slice and removes the generated file.
// If the snapshot file is not produced, it returns an error.
func (cli *Cli) Snapshot(ctx context.Context, file string, opts SnapshotOptions) ([]byte, error) {
	if err := cli.ensureInstalledOnce(ctx); err != nil {
		return nil, fmt.Errorf("ensuring bicep is installed: %w", err)
	}

	args := []string{"snapshot", file}
	if opts.Mode != "" {
		args = append(args, "--mode", opts.Mode)
	}
	if opts.TenantID != "" {
		args = append(args, "--tenant-id", opts.TenantID)
	}
	if opts.SubscriptionID != "" {
		args = append(args, "--subscription-id", opts.SubscriptionID)
	}
	if opts.ResourceGroup != "" {
		args = append(args, "--resource-group", opts.ResourceGroup)
	}
	if opts.Location != "" {
		args = append(args, "--location", opts.Location)
	}
	if opts.DeploymentName != "" {
		args = append(args, "--deployment-name", opts.DeploymentName)
	}

	if _, err := cli.runCommand(ctx, nil, args...); err != nil {
		return nil, fmt.Errorf("failed running bicep snapshot: %w", err)
	}

	// The snapshot output file is <file-without-ext>.snapshot.json in the same directory.
	snapshotFile := strings.TrimSuffix(file, filepath.Ext(file)) + ".snapshot.json"

	data, err := os.ReadFile(snapshotFile)
	if err != nil {
		return nil, fmt.Errorf("reading snapshot file %s: %w", snapshotFile, err)
	}

	if err := os.Remove(snapshotFile); err != nil {
		log.Printf("warning: failed to remove snapshot file %s: %v", snapshotFile, err)
	}

	return data, nil
}

func (cli *Cli) runCommand(ctx context.Context, env []string, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs(cli.path, args...)
	if env != nil {
		runArgs = runArgs.WithEnv(env)
	}
	return cli.runner.Run(ctx, runArgs)
}
