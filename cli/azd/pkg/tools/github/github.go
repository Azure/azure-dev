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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type GitHubCli interface {
	tools.ExternalTool
	GetAuthStatus(ctx context.Context, hostname string) (AuthStatus, error)
	ListSecrets(ctx context.Context, repo string) ([]string, error)
	ListVariables(ctx context.Context, repo string) ([]string, error)
	SetSecret(ctx context.Context, repo string, name string, value string) error
	DeleteSecret(ctx context.Context, repo string, name string) error
	SetVariable(ctx context.Context, repoSlug string, name string, value string) error
	DeleteVariable(ctx context.Context, repoSlug string, name string) error
	Login(ctx context.Context, hostname string) error
	ListRepositories(ctx context.Context) ([]GhCliRepository, error)
	ViewRepository(ctx context.Context, name string) (GhCliRepository, error)
	CreatePrivateRepository(ctx context.Context, name string) error
	GetGitProtocolType(ctx context.Context) (string, error)
	GitHubActionsExists(ctx context.Context, repoSlug string) (bool, error)
	BinaryPath() string
}

func NewGitHubCli(ctx context.Context, console input.Console, commandRunner exec.CommandRunner) (GitHubCli, error) {
	return newGitHubCliImplementation(ctx, console, commandRunner, http.DefaultClient, downloadGh, extractGhCli)
}

// GitHubCliVersion is the minimum version of GitHub cli that we require (and the one we fetch when we fetch bicep on
// behalf of a user).
var GitHubCliVersion semver.Version = semver.MustParse("2.28.0")

// newGitHubCliImplementation is like NewGitHubCli but allows providing a custom transport to use when downloading the
// GitHub CLI, for testing purposes.
func newGitHubCliImplementation(
	ctx context.Context,
	console input.Console,
	commandRunner exec.CommandRunner,
	transporter policy.Transporter,
	acquireGitHubCliImpl getGitHubCliImplementation,
	extractImplementation extractGitHubCliFromFileImplementation,
) (GitHubCli, error) {
	if override := os.Getenv("AZD_GH_CLI_TOOL_PATH"); override != "" {
		log.Printf("using external github cli tool: %s", override)
		cli := &ghCli{
			path:          override,
			commandRunner: commandRunner,
		}
		cli.logVersion(ctx)

		return cli, nil
	}

	githubCliPath, err := azdGithubCliPath()
	if err != nil {
		return nil, fmt.Errorf("getting github cli default path: %w", err)
	}

	if _, err = os.Stat(githubCliPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("getting file information from github cli default path: %w", err)
	}
	var installGhCli bool
	if errors.Is(err, os.ErrNotExist) || !expectedVersionInstalled(ctx, commandRunner, githubCliPath) {
		installGhCli = true
	}
	if installGhCli {
		if err := os.MkdirAll(filepath.Dir(githubCliPath), osutil.PermissionDirectory); err != nil {
			return nil, fmt.Errorf("creating github cli default path: %w", err)
		}

		msg := "setting up github connection"
		console.ShowSpinner(ctx, msg, input.Step)
		err = acquireGitHubCliImpl(ctx, transporter, GitHubCliVersion, extractImplementation, githubCliPath)
		console.StopSpinner(ctx, "", input.Step)
		if err != nil {
			return nil, fmt.Errorf("setting up github connection: %w", err)
		}
	}

	cli := &ghCli{
		path:          githubCliPath,
		commandRunner: commandRunner,
	}
	cli.logVersion(ctx)
	return cli, nil
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

type ghCli struct {
	commandRunner exec.CommandRunner
	path          string
}

func (cli *ghCli) CheckInstalled(ctx context.Context) error {
	return nil
}

func expectedVersionInstalled(ctx context.Context, commandRunner exec.CommandRunner, binaryPath string) bool {
	ghVersion, err := tools.ExecuteCommand(ctx, commandRunner, binaryPath, "--version")
	if err != nil {
		log.Printf("checking %s version: %s", cGhToolName, err.Error())
		return false
	}
	ghSemver, err := tools.ExtractVersion(ghVersion)
	if err != nil {
		log.Printf("converting to semver version fails: %s", err.Error())
		return false
	}
	if ghSemver.LT(GitHubCliVersion) {
		log.Printf("Found gh cli version %s. Expected version: %s.", ghSemver.String(), GitHubCliVersion.String())
		return false
	}
	return true
}

const cGhToolName = "GitHub CLI"

func (cli *ghCli) Name() string {
	return cGhToolName
}

func (cli *ghCli) BinaryPath() string {
	return cli.path
}

func (cli *ghCli) InstallUrl() string {
	return "https://aka.ms/azure-dev/github-cli-install"
}

// The result from calling GetAuthStatus
type AuthStatus struct {
	LoggedIn bool
}

func (cli *ghCli) GetAuthStatus(ctx context.Context, hostname string) (AuthStatus, error) {
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

func (cli *ghCli) Login(ctx context.Context, hostname string) error {
	runArgs := cli.newRunArgs("auth", "login", "--hostname", hostname, "--scopes", "repo,workflow").
		WithInteractive(true)

	_, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed running gh auth login: %w", err)
	}

	return nil
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

func (cli *ghCli) ListSecrets(ctx context.Context, repoSlug string) ([]string, error) {
	runArgs := cli.newRunArgs("-R", repoSlug, "secret", "list")
	output, err := cli.run(ctx, runArgs)
	if err != nil {
		return nil, fmt.Errorf("failed running gh secret list: %w", err)
	}
	return ghOutputToList(output.Stdout), nil
}

func (cli *ghCli) ListVariables(ctx context.Context, repoSlug string) ([]string, error) {
	runArgs := cli.newRunArgs("-R", repoSlug, "variable", "list")
	output, err := cli.run(ctx, runArgs)
	if err != nil {
		return nil, fmt.Errorf("failed running gh secret list: %w", err)
	}
	return ghOutputToList(output.Stdout), nil
}

func (cli *ghCli) SetSecret(ctx context.Context, repoSlug string, name string, value string) error {
	runArgs := cli.newRunArgs("-R", repoSlug, "secret", "set", name).WithStdIn(strings.NewReader(value))
	_, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh secret set: %w", err)
	}
	return nil
}

func (cli *ghCli) SetVariable(ctx context.Context, repoSlug string, name string, value string) error {
	runArgs := cli.newRunArgs("-R", repoSlug, "variable", "set", name).WithStdIn(strings.NewReader(value))
	_, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh variable set: %w", err)
	}
	return nil
}

func (cli *ghCli) DeleteSecret(ctx context.Context, repoSlug string, name string) error {
	runArgs := cli.newRunArgs("-R", repoSlug, "secret", "delete", name)
	_, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh secret delete: %w", err)
	}
	return nil
}

func (cli *ghCli) DeleteVariable(ctx context.Context, repoSlug string, name string) error {
	runArgs := cli.newRunArgs("-R", repoSlug, "variable", "delete", name)
	_, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh variable delete: %w", err)
	}
	return nil
}

// cGhCliVersionRegexp fetches the version number from the output of gh --version, which looks like this:
//
// gh version 2.6.0 (2022-03-15)
// https://github.com/cli/cli/releases/tag/v2.6.0
var cGhCliVersionRegexp = regexp.MustCompile(`gh version ([0-9]+\.[0-9]+\.[0-9]+)`)

// logVersion writes the version of the GitHub CLI to the debug log for diagnostics purposes, or an error if
// it could not be determined
func (cli *ghCli) logVersion(ctx context.Context) {
	if ver, err := cli.extractVersion(ctx); err == nil {
		log.Printf("github cli version: %s", ver)
	} else {
		log.Printf("could not determine github cli version: %s", err)
	}
}

// extractVersion gets the version of the GitHub CLI, from the output of `gh --version`
func (cli *ghCli) extractVersion(ctx context.Context) (string, error) {
	runArgs := cli.newRunArgs("--version")
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("error running gh --version: %w", err)
	}

	matches := cGhCliVersionRegexp.FindStringSubmatch(res.Stdout)
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

func (cli *ghCli) ListRepositories(ctx context.Context) ([]GhCliRepository, error) {
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

func (cli *ghCli) ViewRepository(ctx context.Context, name string) (GhCliRepository, error) {
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

func (cli *ghCli) CreatePrivateRepository(ctx context.Context, name string) error {
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

func (cli *ghCli) GetGitProtocolType(ctx context.Context) (string, error) {
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
func (cli *ghCli) GitHubActionsExists(ctx context.Context, repoSlug string) (bool, error) {
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

func (cli *ghCli) newRunArgs(args ...string) exec.RunArgs {

	runArgs := exec.NewRunArgs(cli.path, args...)
	if RunningOnCodespaces() {
		runArgs = runArgs.WithEnv([]string{"GITHUB_TOKEN=", "GH_TOKEN="})
	}

	return runArgs
}

func (cli *ghCli) run(ctx context.Context, runArgs exec.RunArgs) (exec.RunResult, error) {
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
	"You are not logged into any GitHub hosts. Run gh auth login to authenticate.",
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
			ghCliFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(fileHeader.Mode))
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

	// example: https://github.com/cli/cli/releases/download/v2.28.0/gh_2.28.0_linux_arm64.rpm
	ghReleaseUrl := fmt.Sprintf("https://github.com/cli/cli/releases/download/v%s/%s", ghVersion, releaseName)

	log.Printf("downloading github cli release %s -> %s", ghReleaseUrl, releaseName)

	spanCtx, span := tracing.Start(ctx, events.GitHubCliInstallEvent)
	defer span.End()

	req, err := http.NewRequestWithContext(spanCtx, "GET", ghReleaseUrl, nil)
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
