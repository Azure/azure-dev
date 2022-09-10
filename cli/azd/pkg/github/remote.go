package github

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
)

var ErrRemoteHostIsNotGitHub = errors.New("not a github host")

var gitHubRemoteGitUrlRegex = regexp.MustCompile(`^git@github\.com:(.*?)(?:\.git)?$`)
var gitHubRemoteHttpsUrlRegex = regexp.MustCompile(`^https://(?:www\.)?github\.com/(.*?)(?:\.git)?$`)

// ensureRemote ensures that a git repository located at
// `repositoryPath` has a remote and it is configured with a
// URL for a repository hosted on GitHub.
// It returns the repository slug `(<organization>/<repo>)`.
func EnsureRemote(ctx context.Context, repositoryPath string, remoteName string, gitCli git.GitCli) (string, error) {
	remoteUrl, err := gitCli.GetRemoteUrl(ctx, repositoryPath, remoteName)
	if err != nil {
		return "", fmt.Errorf("failed to get remote url: %w", err)
	}

	slug, err := GetSlugForRemote(remoteUrl)
	if errors.Is(err, ErrRemoteHostIsNotGitHub) {
		return "", fmt.Errorf("remote `%s` is not a GitHub repository", remoteName)
	} else if err != nil {
		return "", fmt.Errorf("failed to determine project slug for remote %s: %w", remoteName, err)
	}

	return slug, nil
}

func GetSlugForRemote(remoteUrl string) (string, error) {
	for _, r := range []*regexp.Regexp{gitHubRemoteGitUrlRegex, gitHubRemoteHttpsUrlRegex} {
		captures := r.FindStringSubmatch(remoteUrl)
		if captures != nil {
			return captures[1], nil
		}
	}

	return "", ErrRemoteHostIsNotGitHub
}
