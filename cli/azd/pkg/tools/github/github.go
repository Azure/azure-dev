// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
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

var _ tools.ExternalTool = (*Cli)(nil)

// NewGitHubCli creates a new GitHub CLI wrapper. The CLI is not yet installed; call EnsureInstalled
// before using methods that require the gh binary.
func NewGitHubCli(console input.Console, commandRunner exec.CommandRunner) *Cli {
	return newGitHubCliImplementation(console, commandRunner, http.DefaultClient, downloadGh, extractGhCli)
}

// Version is the minimum version of GitHub cli that we require (and the one we fetch when we fetch gh on
// behalf of a user).
var Version semver.Version = semver.MustParse("2.86.0")

// newGitHubCliImplementation is like NewGitHubCli but allows providing a custom transport for testing.
func newGitHubCliImplementation(
	console input.Console,
	commandRunner exec.CommandRunner,
	transporter policy.Transporter,
	acquireGitHubCliImpl getGitHubCliImplementation,
	extractImplementation extractGitHubCliFromFileImplementation,
) *Cli {
	return &Cli{
		commandRunner:         commandRunner,
		console:               console,
		transporter:           transporter,
		acquireGitHubCliImpl:  acquireGitHubCliImpl,
		extractImplementation: extractImplementation,
	}
}

// azdGithubCliPath returns the path where we store our local copy of github cli ($AZD_CONFIG_DIR/bin).
func azdGithubCliPath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "bin", ghCliName()), nil
}

func ghCliName() string {
	if runtime.GOOS == "windows" {
		return "gh.exe"
	}
	return "gh"
}

var (
	ErrGitHubCliNotLoggedIn = errors.New("gh cli is not logged in")
	ErrUserNotAuthorized    = errors.New("user is not authorized. " +
		"Try running gh auth refresh with the required scopes to request additional authorization")
	ErrRepositoryNameInUse = errors.New("repository name already in use")

	// The hostname of the public GitHub service.
	GitHubHostName = "github.com"

	// Environment variable that gh cli uses for auth token overrides
	TokenEnvVars = []string{"GITHUB_TOKEN", "GH_TOKEN"}
)

type Cli struct {
	commandRunner         exec.CommandRunner
	console               input.Console
	transporter           policy.Transporter
	acquireGitHubCliImpl  getGitHubCliImplementation
	extractImplementation extractGitHubCliFromFileImplementation
	path                  string

	installOnce sync.Once
	installErr  error
}

// EnsureInstalled checks if GitHub CLI is available and downloads/upgrades if needed.
// This is safe to call multiple times; installation only happens once.
// Should be called with a request-scoped context before first use.
func (cli *Cli) EnsureInstalled(ctx context.Context) error {
	cli.installOnce.Do(func() {
		cli.installErr = cli.ensureInstalled(ctx)
	})
	return cli.installErr
}

func (cli *Cli) ensureInstalled(ctx context.Context) error {
	if override := os.Getenv("AZD_GH_TOOL_PATH"); override != "" {
		log.Printf("using external github cli tool: %s", override)
		cli.path = override
		cli.logVersion(ctx)
		return nil
	}

	githubCliPath, err := azdGithubCliPath()
	if err != nil {
		return fmt.Errorf("getting github cli default path: %w", err)
	}

	if _, err = os.Stat(githubCliPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("getting file information from github cli default path: %w", err)
	}
	var installGhCli bool
	if errors.Is(err, os.ErrNotExist) || !expectedVersionInstalled(ctx, cli.commandRunner, githubCliPath) {
		installGhCli = true
	}
	if installGhCli {
		if err := os.MkdirAll(filepath.Dir(githubCliPath), osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("creating github cli default path: %w", err)
		}

		msg := "setting up github connection"
		cli.console.ShowSpinner(ctx, msg, input.Step)
		err = cli.acquireGitHubCliImpl(ctx, cli.transporter, Version, cli.extractImplementation, githubCliPath)
		cli.console.StopSpinner(ctx, "", input.Step)
		if err != nil {
			return fmt.Errorf("setting up github connection: %w", err)
		}
	}

	cli.path = githubCliPath
	cli.logVersion(ctx)
	return nil
}

func (cli *Cli) CheckInstalled(ctx context.Context) error {
	return cli.EnsureInstalled(ctx)
}

func expectedVersionInstalled(ctx context.Context, commandRunner exec.CommandRunner, binaryPath string) bool {
	ghVersion, err := tools.ExecuteCommand(ctx, commandRunner, binaryPath, "--version")
	if err != nil {
		log.Printf("checking GitHub CLI version: %v", err)
		return false
	}
	ghSemver, err := tools.ExtractVersion(ghVersion)
	if err != nil {
		log.Printf("converting to semver version fails: %v", err)
		return false
	}
	if ghSemver.LT(Version) {
		log.Printf("Found gh cli version %s. Expected version: %s.", ghSemver.String(), Version.String())
		return false
	}
	return true
}

func (cli *Cli) Name() string {
	return "GitHub CLI"
}

func (cli *Cli) BinaryPath() string {
	return cli.path
}

func (cli *Cli) InstallUrl() string {
	return "https://aka.ms/azure-dev/github-cli-install"
}

// The result from calling GetAuthStatus
type AuthStatus struct {
	LoggedIn bool
}

func (cli *Cli) GetAuthStatus(ctx context.Context, hostname string) (AuthStatus, error) {
	runArgs := cli.newRunArgs("auth", "status", "--hostname", hostname)
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err == nil {
		authResult := AuthStatus{LoggedIn: true}
		return authResult, nil
	}

	if isGhCliNotLoggedInMessageRegex.MatchString(res.Stderr) {
		return AuthStatus{}, nil
	} else if notLoggedIntoAnyGitHubHostsMessageRegex.MatchString(res.Stderr) {
		return AuthStatus{}, nil
	}

	return AuthStatus{}, fmt.Errorf("failed running gh auth status: %w", err)
}

func (cli *Cli) Login(ctx context.Context, hostname string) error {
	runArgs := cli.newRunArgs("auth", "login", "--hostname", hostname, "--scopes", "repo,workflow").
		WithInteractive(true)

	_, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed running gh auth login: %w", err)
	}

	return nil
}

// ApiCallOptions represent the options for the ApiCall method.
type ApiCallOptions struct {
	Headers []string
}

// ApiCall uses gh cli to call https://api.<hostname>/<path>.
func (cli *Cli) ApiCall(ctx context.Context, hostname, path string, options ApiCallOptions) (string, error) {
	url := fmt.Sprintf("https://api.%s%s", hostname, path)
	args := []string{"api", url}
	for _, header := range options.Headers {
		args = append(args, "-H", header)
	}
	// application/vnd.github.raw makes the API return the raw content of the file
	runArgs := cli.newRunArgs(args...)
	result, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("failed running gh api: %s: %w", url, err)
	}

	return result.Stdout, nil
}

func ghOutputToList(output string) []string {
	lines := strings.Split(output, "\n")
	result := make([]string, len(lines)-1)
	for i, line := range lines {
		if line == "" {
			continue
		}
		valueParts := strings.Split(line, "\t")
		result[i] = valueParts[0]
	}
	return result
}

func ghOutputToMap(output string) (map[string]string, error) {
	lines := strings.Split(output, "\n")
	result := map[string]string{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		valueParts := strings.Split(line, "\t")
		if len(valueParts) < 2 {
			return nil, fmt.Errorf("unexpected format to parse string to map: %s", line)
		}
		result[valueParts[0]] = valueParts[1]
	}
	return result, nil
}

func (cli *Cli) ListSecrets(ctx context.Context, repoSlug string) ([]string, error) {
	runArgs := cli.newRunArgs("-R", repoSlug, "secret", "list")
	output, err := cli.run(ctx, runArgs)
	if err != nil {
		return nil, fmt.Errorf("failed running gh secret list: %w", err)
	}
	return ghOutputToList(output.Stdout), nil
}

type ListVariablesOptions struct {
	Environment string
}

//
//nolint:lll
func (cli *Cli) ListVariables(
	ctx context.Context,
	repoSlug string,
	options *ListVariablesOptions,
) (map[string]string, error) {
	args := []string{"-R", repoSlug, "variable", "list"}

	if options != nil && options.Environment != "" {
		args = append(args, "--env", options.Environment)
	}

	runArgs := cli.newRunArgs(args...)
	output, err := cli.run(ctx, runArgs)
	if err != nil {
		return nil, fmt.Errorf("failed running gh secret list: %w", err)
	}
	return ghOutputToMap(output.Stdout)
}

func (cli *Cli) SetSecret(ctx context.Context, repoSlug string, name string, value string) error {
	runArgs := cli.newRunArgs("-R", repoSlug, "secret", "set", name).WithStdIn(strings.NewReader(value))
	_, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh secret set: %w", err)
	}
	return nil
}

type SetVariableOptions struct {
	Environment string
}

//
//nolint:lll
func (cli *Cli) SetVariable(
	ctx context.Context,
	repoSlug string,
	name string,
	value string,
	options *SetVariableOptions,
) error {
	args := []string{"-R", repoSlug, "variable", "set", name}

	if options != nil && options.Environment != "" {
		args = append(args, "--env", options.Environment)
	}

	runArgs := cli.newRunArgs(args...).WithStdIn(strings.NewReader(value))
	_, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh variable set: %w", err)
	}
	return nil
}

func (cli *Cli) DeleteSecret(ctx context.Context, repoSlug string, name string) error {
	runArgs := cli.newRunArgs("-R", repoSlug, "secret", "delete", name)
	_, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh secret delete: %w", err)
	}
	return nil
}

func (cli *Cli) DeleteVariable(ctx context.Context, repoSlug string, name string) error {
	runArgs := cli.newRunArgs("-R", repoSlug, "variable", "delete", name)
	_, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh variable delete: %w", err)
	}
	return nil
}

// ghCliVersionRegexp fetches the version number from the output of gh --version, which looks like this:
//
// gh version 2.6.0 (2022-03-15)
// https://github.com/cli/cli/releases/tag/v2.6.0
var ghCliVersionRegexp = regexp.MustCompile(`gh version ([0-9]+\.[0-9]+\.[0-9]+)`)

// logVersion writes the version of the GitHub CLI to the debug log for diagnostics purposes, or an error if
// it could not be determined
func (cli *Cli) logVersion(ctx context.Context) {
	if ver, err := cli.extractVersion(ctx); err == nil {
		log.Printf("github cli version: %s", ver)
	} else {
		log.Printf("could not determine github cli version: %s", err)
	}
}

// extractVersion gets the version of the GitHub CLI, from the output of `gh --version`
func (cli *Cli) extractVersion(ctx context.Context) (string, error) {
	runArgs := cli.newRunArgs("--version")
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("error running gh --version: %w", err)
	}

	matches := ghCliVersionRegexp.FindStringSubmatch(res.Stdout)
	if len(matches) != 2 {
		return "", fmt.Errorf("could not extract version from output: %s", res.Stdout)
	}
	return matches[1], nil
}

type GhCliRepository struct {
	// The slug for a repository (formatted as "<owner>/<name>")
	NameWithOwner string
	// The Url for the HTTPS endpoint for the repository
	HttpsUrl string `json:"url"`
	// The Url for the SSH endpoint for the repository
	SshUrl string
}

func (cli *Cli) ListRepositories(ctx context.Context) ([]GhCliRepository, error) {
	runArgs := cli.newRunArgs("repo", "list", "--no-archived", "--json", "nameWithOwner,url,sshUrl")
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return nil, fmt.Errorf("failed running gh repo list: %w", err)
	}

	var repos []GhCliRepository

	if err := json.Unmarshal([]byte(res.Stdout), &repos); err != nil {
		return nil, fmt.Errorf("could not unmarshal output as a []GhCliRepository: %w, output: %s", err, res.Stdout)
	}

	return repos, nil
}

func (cli *Cli) ViewRepository(ctx context.Context, name string) (GhCliRepository, error) {
	runArgs := cli.newRunArgs("repo", "view", name, "--json", "nameWithOwner,url,sshUrl")
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return GhCliRepository{}, fmt.Errorf("failed running gh repo list: %w", err)
	}

	var repo GhCliRepository

	if err := json.Unmarshal([]byte(res.Stdout), &repo); err != nil {
		return GhCliRepository{},
			fmt.Errorf("could not unmarshal output as a GhCliRepository: %w, output: %s", err, res.Stdout)
	}

	return repo, nil
}

func (cli *Cli) CreatePrivateRepository(ctx context.Context, name string) error {
	runArgs := cli.newRunArgs("repo", "create", name, "--private")
	res, err := cli.run(ctx, runArgs)
	if repositoryNameInUseRegex.MatchString(res.Stderr) {
		return ErrRepositoryNameInUse
	} else if err != nil {
		return fmt.Errorf("failed running gh repo create: %w", err)
	}

	return nil
}

const (
	GitSshProtocolType   = "ssh"
	GitHttpsProtocolType = "https"
)

func (cli *Cli) GetGitProtocolType(ctx context.Context) (string, error) {
	runArgs := cli.newRunArgs("config", "get", "git_protocol")
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("failed running gh config get git_protocol: %w", err)
	}

	return strings.TrimSpace(res.Stdout), nil
}

type GitHubActionsResponse struct {
	TotalCount int `json:"total_count"`
}

// GitHubActionsExists gets the information from upstream about the workflows and
// return true if there is at least one workflow in the repo.
func (cli *Cli) GitHubActionsExists(ctx context.Context, repoSlug string) (bool, error) {
	runArgs := cli.newRunArgs("api", "/repos/"+repoSlug+"/actions/workflows")
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return false, fmt.Errorf("getting github actions: %w", err)
	}
	var jsonResponse GitHubActionsResponse
	if err := json.Unmarshal([]byte(res.Stdout), &jsonResponse); err != nil {
		return false, fmt.Errorf("could not unmarshal output as a GhActionsResponse: %w, output: %s", err, res.Stdout)
	}
	if jsonResponse.TotalCount == 0 {
		return false, nil
	}
	return true, nil
}

func (cli *Cli) CreateEnvironmentIfNotExist(ctx context.Context, repoName string, envName string) error {
	// Doc: https://docs.github.com/en/rest/deployments/environments?apiVersion=2022-11-28#create-or-update-an-environment
	runArgs := cli.newRunArgs("api",
		"-X", "PUT",
		fmt.Sprintf("/repos/%s/environments/%s", repoName, envName),
		"-H", "Accept: application/vnd.github+json",
	)

	_, err := cli.run(ctx, runArgs)
	return err
}

func (cli *Cli) DeleteEnvironment(ctx context.Context, repoName string, envName string) error {
	// Doc: https://docs.github.com/en/rest/deployments/environments?apiVersion=2022-11-28#delete-an-environment
	runArgs := cli.newRunArgs("api",
		"-X", "DELETE",
		fmt.Sprintf("/repos/%s/environments/%s", repoName, envName),
		"-H", "Accept: application/vnd.github+json",
	)

	_, err := cli.run(ctx, runArgs)
	return err
}

func (cli *Cli) newRunArgs(args ...string) exec.RunArgs {

	runArgs := exec.NewRunArgs(cli.path, args...)
	if RunningOnCodespaces() {
		runArgs = runArgs.WithEnv([]string{"GITHUB_TOKEN=", "GH_TOKEN="})
	}

	return runArgs
}

func (cli *Cli) run(ctx context.Context, runArgs exec.RunArgs) (exec.RunResult, error) {
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if isGhCliNotLoggedInMessageRegex.MatchString(res.Stderr) {
		return res, ErrGitHubCliNotLoggedIn
	}

	if isUserNotAuthorizedMessageRegex.MatchString(res.Stderr) {
		return res, ErrUserNotAuthorized
	}

	return res, err
}

//nolint:lll
var isGhCliNotLoggedInMessageRegex = regexp.MustCompile(
	"(To authenticate, please run `gh auth login`\\.)|(Try authenticating with:  gh auth login)|(To re-authenticate, run: gh auth login)|(To get started with GitHub CLI, please run:  gh auth login)",
)
var repositoryNameInUseRegex = regexp.MustCompile(`GraphQL: Name already exists on this account \(createRepository\)`)

var notLoggedIntoAnyGitHubHostsMessageRegex = regexp.MustCompile(
	"You are not logged into any GitHub hosts.",
)

var isUserNotAuthorizedMessageRegex = regexp.MustCompile(
	"HTTP 403: Resource not accessible by integration",
)

func extractFromZip(src, dst string) (string, error) {
	zipReader, err := zip.OpenReader(src)
	if err != nil {
		return "", err
	}

	log.Printf("extract from zip %s", src)
	defer zipReader.Close()

	var extractedAt string
	for _, file := range zipReader.File {
		fileName := file.FileInfo().Name()
		if !file.FileInfo().IsDir() && fileName == ghCliName() {
			log.Printf("found cli at: %s", file.Name)
			fileReader, err := file.Open()
			if err != nil {
				return extractedAt, err
			}
			filePath := filepath.Join(dst, fileName)
			ghCliFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
			if err != nil {
				return extractedAt, err
			}
			defer ghCliFile.Close()
			/* #nosec G110 - decompression bomb false positive */
			_, err = io.Copy(ghCliFile, fileReader)
			if err != nil {
				return extractedAt, err
			}
			extractedAt = filePath
			break
		}
	}
	if extractedAt != "" {
		log.Printf("extracted to: %s", extractedAt)
		return extractedAt, nil
	}
	return extractedAt, fmt.Errorf("github cli binary was not found within the zip file")
}
func extractFromTar(src, dst string) (string, error) {
	gzFile, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer gzFile.Close()

	gzReader, err := gzip.NewReader(gzFile)
	if err != nil {
		return "", err
	}
	defer gzReader.Close()

	var extractedAt string
	// tarReader doesn't need to be closed as it is closed by the gz reader
	tarReader := tar.NewReader(gzReader)
	for {
		fileHeader, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return extractedAt, fmt.Errorf("did not find gh cli within tar file")
		}
		if fileHeader == nil {
			continue
		}
		if err != nil {
			return extractedAt, err
		}
		// Tha name contains the path, remove it
		fileNameParts := strings.Split(fileHeader.Name, "/")
		fileName := fileNameParts[len(fileNameParts)-1]
		// cspell: disable-next-line `Typeflag` is comming fron *tar.Header
		if fileHeader.Typeflag == tar.TypeReg && fileName == "gh" {
			filePath := filepath.Join(dst, fileName)
			ghCliFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileHeader.FileInfo().Mode())
			if err != nil {
				return extractedAt, err
			}
			defer ghCliFile.Close()
			/* #nosec G110 - decompression bomb false positive */
			_, err = io.Copy(ghCliFile, tarReader)
			if err != nil {
				return extractedAt, err
			}
			extractedAt = filePath
			break
		}
	}
	if extractedAt != "" {
		return extractedAt, nil
	}
	return extractedAt, fmt.Errorf("extract from tar error. Extraction ended in unexpected state.")
}

// extractGhCli gets the Github cli from either a zip or a tar.gz
func extractGhCli(src, dst string) (string, error) {
	if strings.HasSuffix(src, ".zip") {
		return extractFromZip(src, dst)
	} else if strings.HasSuffix(src, ".tar.gz") {
		return extractFromTar(src, dst)
	}
	return "", fmt.Errorf("Unknown format while trying to extract")
}

// getGitHubCliImplementation defines the contract function to acquire the GitHub cli.
// The `outputPath` is the destination where the github cli is place it.
type getGitHubCliImplementation func(
	ctx context.Context,
	transporter policy.Transporter,
	ghVersion semver.Version,
	extractImplementation extractGitHubCliFromFileImplementation,
	outputPath string) error

// extractGitHubCliFromFileImplementation defines how the cli is extracted
type extractGitHubCliFromFileImplementation func(src, dst string) (string, error)

// downloadGh downloads a given version of GitHub cli from the release site.
func downloadGh(
	ctx context.Context,
	transporter policy.Transporter,
	ghVersion semver.Version,
	extractImplementation extractGitHubCliFromFileImplementation,
	path string) error {

	binaryName := func(platform string) string {
		return fmt.Sprintf("gh_%s_%s", ghVersion, platform)
	}

	systemArch := runtime.GOARCH
	// arm and x86 not supported (similar to bicep)
	var releaseName string
	switch runtime.GOOS {
	case "windows":
		releaseName = binaryName(fmt.Sprintf("windows_%s.zip", systemArch))
	case "darwin":
		releaseName = binaryName(fmt.Sprintf("macOS_%s.zip", systemArch))
	case "linux":
		releaseName = binaryName(fmt.Sprintf("linux_%s.tar.gz", systemArch))
	default:
		return fmt.Errorf("unsupported platform")
	}

	// example: https://github.com/cli/cli/releases/download/v2.86.0/gh_2.86.0_linux_arm64.tar.gz
	ghReleaseUrl := fmt.Sprintf("https://github.com/cli/cli/releases/download/v%s/%s", ghVersion, releaseName)

	log.Printf("downloading github cli release %s -> %s", ghReleaseUrl, releaseName)

	req, err := http.NewRequestWithContext(ctx, "GET", ghReleaseUrl, nil)
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

	tmpPath := filepath.Dir(path)
	compressedRelease, err := os.CreateTemp(tmpPath, releaseName)
	if err != nil {
		return err
	}
	defer func() {
		_ = compressedRelease.Close()
		_ = os.Remove(compressedRelease.Name())
	}()

	if _, err := io.Copy(compressedRelease, resp.Body); err != nil {
		return err
	}
	if err := compressedRelease.Close(); err != nil {
		return err
	}

	// change file name from temporal name to the final name, as the download has completed
	compressedFileName := filepath.Join(tmpPath, releaseName)
	if err := osutil.Rename(ctx, compressedRelease.Name(), compressedFileName); err != nil {
		return err
	}
	defer func() {
		log.Printf("delete %s", compressedFileName)
		_ = os.Remove(compressedFileName)
	}()

	// unzip downloaded file
	log.Printf("extracting file %s", compressedFileName)
	_, err = extractImplementation(compressedFileName, tmpPath)
	if err != nil {
		return err
	}

	return nil
}

// RunningOnCodespaces check if the application is running on codespaces.
func RunningOnCodespaces() bool {
	return os.Getenv("CODESPACES") == "true"
}
