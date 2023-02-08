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
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
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
	// Forces the authentication token mode used by github CLI.
	//
	// If set to TokenSourceFile, environment variables such as GH_TOKEN and GITHUB_TOKEN are ignored.
	ForceConfigureAuth(authMode AuthTokenSource)
	ListSecrets(ctx context.Context, repo string) error
	SetSecret(ctx context.Context, repo string, name string, value string) error
	Login(ctx context.Context, hostname string) error
	ListRepositories(ctx context.Context) ([]GhCliRepository, error)
	ViewRepository(ctx context.Context, name string) (GhCliRepository, error)
	CreatePrivateRepository(ctx context.Context, name string) error
	GetGitProtocolType(ctx context.Context) (string, error)
	GitHubActionsExists(ctx context.Context, repoSlug string) (bool, error)
}

func NewGitHubCli(ctx context.Context, console input.Console, commandRunner exec.CommandRunner) (GitHubCli, error) {
	return newGitHubCliImplementation(ctx, console, commandRunner, http.DefaultClient, downloadGh, extractGhCli)
}

// cGitHubCliVersion is the minimum version of GitHub cli that we require (and the one we fetch when we fetch bicep on
// behalf of a user).
var cGitHubCliVersion semver.Version = semver.MustParse("2.22.1")

// newGitHubCliImplementation is like NewGitHubCli but allows providing a custom transport to use when downloading the
// GitHub CLI, for testing purposes.
func newGitHubCliImplementation(
	ctx context.Context,
	console input.Console,
	commandRunner exec.CommandRunner,
	transporter policy.Transporter,
	acquireGitHubCliImpl GetGitHubCliImplementation,
	extractImplementation ExtractGitHubCliFromFileImplementation,
) (GitHubCli, error) {
	if override := os.Getenv("AZD_GH_CLI_TOOL_PATH"); override != "" {
		log.Printf("using external github cli tool: %s", override)

		return &ghCli{
			path:          override,
			commandRunner: commandRunner,
		}, nil
	}

	githubCliPath, err := azdGithubCliPath()
	if err != nil {
		return nil, fmt.Errorf("finding github cli: %w", err)
	}

	if _, err = os.Stat(githubCliPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("finding github cli: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(githubCliPath), osutil.PermissionDirectory); err != nil {
			return nil, fmt.Errorf("downloading github cli: %w", err)
		}

		msg := "Downloading Github cli"
		console.ShowSpinner(ctx, msg, input.Step)
		err = acquireGitHubCliImpl(ctx, transporter, cGitHubCliVersion, extractImplementation, githubCliPath)
		console.StopSpinner(ctx, "", input.Step)
		if err != nil {
			return nil, fmt.Errorf("downloading github cli: %w", err)
		}
	}

	cli := &ghCli{
		path:          githubCliPath,
		commandRunner: commandRunner,
	}

	return cli, nil
}

// azdGithubCliPath returns the path where we store our local copy of github cli ($AZD_CONFIG_DIR/bin).
func azdGithubCliPath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		return filepath.Join(configDir, "bin", "gh.exe"), nil
	}

	return filepath.Join(configDir, "bin", "gh"), nil
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

	// Override token-specific environment variables, in format of key=value.
	//
	// This is used to unset the value of GITHUB_TOKEN, GH_TOKEN environment variables for gh cli calls,
	// allowing a new token to be generated via gh auth login or gh auth refresh.
	overrideTokenEnv []string
}

func (cli *ghCli) CheckInstalled(ctx context.Context) (bool, error) {
	return true, nil
}

func (cli *ghCli) Name() string {
	return "GitHub CLI"
}

func (cli *ghCli) InstallUrl() string {
	return "https://aka.ms/azure-dev/github-cli-install"
}

// The result from calling GetAuthStatus
type AuthStatus struct {
	LoggedIn    bool
	TokenSource AuthTokenSource
}

// The source of the auth token used by `gh` CLI
type AuthTokenSource int

const (
	TokenSourceFile AuthTokenSource = iota
	// See TokenEnvVars for token env vars
	TokenSourceEnvVar
)

func (cli *ghCli) GetAuthStatus(ctx context.Context, hostname string) (AuthStatus, error) {
	runArgs := cli.newRunArgs("auth", "status", "--hostname", hostname)
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if res.ExitCode == 0 {
		authResult := AuthStatus{TokenSource: TokenSourceFile, LoggedIn: true}
		if isEnvVarTokenSource(res.Stderr) {
			authResult.TokenSource = TokenSourceEnvVar
		}
		return authResult, nil
	} else if isGhCliNotLoggedInMessageRegex.MatchString(res.Stderr) {
		return AuthStatus{}, nil
	} else if notLoggedIntoAnyGitHubHostsMessageRegex.MatchString(res.Stderr) {
		return AuthStatus{}, nil
	} else if err != nil {
		return AuthStatus{}, fmt.Errorf("failed running gh auth status %s: %w", res.String(), err)
	}

	return AuthStatus{}, errors.New("could not determine auth status")
}

func (cli *ghCli) Login(ctx context.Context, hostname string) error {
	runArgs := cli.newRunArgs("auth", "login", "--hostname", hostname).
		WithInteractive(true)

	res, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed running gh auth login %s: %w", res.String(), err)
	}

	return nil
}

func (cli *ghCli) ListSecrets(ctx context.Context, repoSlug string) error {
	runArgs := cli.newRunArgs("-R", repoSlug, "secret", "list")
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh secret list %s: %w", res.String(), err)
	}
	return nil
}

func (cli *ghCli) SetSecret(ctx context.Context, repoSlug string, name string, value string) error {
	runArgs := cli.newRunArgs("-R", repoSlug, "secret", "set", name, "--body", value)
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running gh secret set %s: %w", res.String(), err)
	}
	return nil
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
		return nil, fmt.Errorf("failed running gh repo list %s: %w", res.String(), err)
	}

	var repos []GhCliRepository

	if err := json.Unmarshal([]byte(res.Stdout), &repos); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a []GhCliRepository: %w", res.Stdout, err)
	}

	return repos, nil
}

func (cli *ghCli) ViewRepository(ctx context.Context, name string) (GhCliRepository, error) {
	runArgs := cli.newRunArgs("repo", "view", name, "--json", "nameWithOwner,url,sshUrl")
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return GhCliRepository{}, fmt.Errorf("failed running gh repo list %s: %w", res.String(), err)
	}

	var repo GhCliRepository

	if err := json.Unmarshal([]byte(res.Stdout), &repo); err != nil {
		return GhCliRepository{}, fmt.Errorf("could not unmarshal output %s as a GhCliRepository: %w", res.Stdout, err)
	}

	return repo, nil
}

func (cli *ghCli) CreatePrivateRepository(ctx context.Context, name string) error {
	runArgs := cli.newRunArgs("repo", "create", name, "--private")
	res, err := cli.run(ctx, runArgs)
	if repositoryNameInUseRegex.MatchString(res.Stderr) {
		return ErrRepositoryNameInUse
	} else if err != nil {
		return fmt.Errorf("failed running gh repo create %s: %w", res.String(), err)
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
		return "", fmt.Errorf("failed running gh config get git_protocol %s: %w", res.String(), err)
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
		return false, fmt.Errorf("getting github actions %s: %w", res.String(), err)
	}
	var jsonResponse GitHubActionsResponse
	if err := json.Unmarshal([]byte(res.Stdout), &jsonResponse); err != nil {
		return false, fmt.Errorf("could not unmarshal output %s as a GhActionsResponse: %w", res.Stdout, err)
	}
	if jsonResponse.TotalCount == 0 {
		return false, nil
	}
	return true, nil
}

func (cli *ghCli) ForceConfigureAuth(authMode AuthTokenSource) {
	switch authMode {
	case TokenSourceFile:
		// Unset token environment variables to force file-base auth.
		for _, tokenEnvVarName := range TokenEnvVars {
			cli.overrideTokenEnv = append(cli.overrideTokenEnv, fmt.Sprintf("%v=", tokenEnvVarName))
		}
	case TokenSourceEnvVar:
		// GitHub CLI will always use environment variables first.
		// Therefore, we simply need to clear our environment context override (if any) to force environment variable usage.
		cli.overrideTokenEnv = nil
	default:
		panic(fmt.Sprintf("Unsupported auth mode: %d", authMode))
	}
}

func (cli *ghCli) newRunArgs(args ...string) exec.RunArgs {
	runArgs := exec.NewRunArgs(cli.path, args...)
	if cli.overrideTokenEnv != nil {
		runArgs = runArgs.WithEnv(cli.overrideTokenEnv)
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
var repositoryNameInUseRegex = regexp.MustCompile("GraphQL: Name already exists on this account (createRepository)")

var notLoggedIntoAnyGitHubHostsMessageRegex = regexp.MustCompile(
	"You are not logged into any GitHub hosts. Run gh auth login to authenticate.",
)

var isUserNotAuthorizedMessageRegex = regexp.MustCompile(
	"HTTP 403: Resource not accessible by integration",
)

// Returns true if a login message contains an environment variable sourced token. See `gh environment` for full details
//
// Example matched message:
//
//	✓ Logged in to github.com as USER (GITHUB_TOKEN)
func isEnvVarTokenSource(message string) bool {
	for _, tokenEnvVar := range TokenEnvVars {
		if strings.Contains(message, tokenEnvVar) {
			return true
		}
	}

	return false
}

func extractFromZip(src, dst string) (string, error) {
	zipReader, err := zip.OpenReader(src)
	if err != nil {
		return "", err
	}

	defer zipReader.Close()

	var extractedAt string
	for _, file := range zipReader.File {
		if !file.FileInfo().IsDir() && strings.Contains(file.Name, "gh") {
			fileReader, err := file.Open()
			if err != nil {
				return extractedAt, err
			}
			fileNameParts := strings.Split(file.Name, "/")
			fileName := fileNameParts[len(fileNameParts)-1]
			filePath := filepath.Join(dst, fileName)
			ghCliFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
			if err != nil {
				return extractedAt, err
			}
			defer ghCliFile.Close()
			_, err = io.Copy(ghCliFile, fileReader)
			if err != nil {
				return extractedAt, err
			}
			extractedAt = filePath
			break
		}
	}
	return extractedAt, nil
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
			return extractedAt, fmt.Errorf("did not find gh cli within tar file: %w", err)
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
		if fileHeader.Typeflag == tar.TypeReg && fileName == "gh" {
			filePath := filepath.Join(dst, fileName)
			ghCliFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(fileHeader.Mode))
			if err != nil {
				return extractedAt, err
			}
			defer ghCliFile.Close()
			_, err = io.Copy(ghCliFile, tarReader)
			if err != nil {
				return extractedAt, err
			}
			extractedAt = filePath
			break
		}
	}

	return extractedAt, nil
}

// extractGhCli gets the Github cli from either a zip or a tar.gz
func extractGhCli(src, dst string) (string, error) {
	if strings.Contains(src, ".zip") {
		return extractFromZip(src, dst)
	} else if strings.Contains(src, ".tar.gz") {
		return extractFromTar(src, dst)
	}
	return "nil", fmt.Errorf("Unknown format")
}

// GetGitHubCliImplementation defines the contract function to acquire the GitHub cli.
// The `outputPath` is the destination where the github cli is place it.
type GetGitHubCliImplementation func(
	ctx context.Context,
	transporter policy.Transporter,
	ghVersion semver.Version,
	extractImplementation ExtractGitHubCliFromFileImplementation,
	outputPath string) error

// ExtractGitHubCliFromFileImplementation defines how the cli is extracted
type ExtractGitHubCliFromFileImplementation func(src, dst string) (string, error)

// downloadGh downloads a given version of GitHub cli from the release site.
func downloadGh(
	ctx context.Context,
	transporter policy.Transporter,
	ghVersion semver.Version,
	extractImplementation ExtractGitHubCliFromFileImplementation,
	path string) error {

	// arm and x86 not supported (similar to bicep)
	var releaseName string
	switch runtime.GOOS {
	case "windows":
		releaseName = "gh_2.22.1_windows_amd64.zip"
	case "darwin":
		releaseName = "gh_2.22.1_macOS_amd64.tar.gz"
	case "linux":
		releaseName = "gh_2.22.1_linux_amd64.tar.gz"
	default:
		return fmt.Errorf("unsupported platform")
	}

	//https://github.com/cli/cli/releases/download/v2.22.1/gh_2.22.1_linux_arm64.rpm
	ghReleaseUrl := fmt.Sprintf("https://github.com/cli/cli/releases/download/v%s/%s", ghVersion, releaseName)

	log.Printf("downloading github cli release %s -> %s", ghReleaseUrl, releaseName)

	spanCtx, span := telemetry.GetTracer().Start(ctx, events.GitHubCliInstallEvent)
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

	// unzip downloaded file
	ghCliTemporalPath, err := extractImplementation(compressedRelease.Name(), tmpPath)
	if err != nil {
		return err
	}

	if err := os.Rename(ghCliTemporalPath, path); err != nil {
		return err
	}

	return nil
}
