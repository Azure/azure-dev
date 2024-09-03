package templates

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
)

// newGhTemplateSource creates a new template source from a Github repository.
func newGhTemplateSource(
	ctx context.Context, name string, urlArg string, ghCli *github.Cli, console input.Console) (Source, error) {
	// urlArg validation:
	// - accepts only URLs with the following format:
	//  - https://raw.<hostname>/<owner>/<repo>/<branch>/<path>/<file>.json
	//    - This url comes from a user clicking the `raw` button on a file in a GitHub repository (web view).
	//  - https://<hostname>/<owner>/<repo>/<branch>/<path>/<file>.json
	//    - This url comes from a user browsing GitHub repository and copy-pasting the url from the browser.
	//  - https://api.<hostname>/repos/<owner>/<repo>/contents/<path>/<file>.json
	//    - This url comes from users familiar with the GitHub API. Usually for programmatic registration of templates.

	// Parse the URL to get the hostname
	parsedURL, err := url.Parse(urlArg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}
	hostname := parsedURL.Hostname()
	repoSlug := ""
	filePath := ""
	if strings.HasPrefix(hostname, "raw.") {
		// <hostname>/<owner>/<repo>/<branch>/[path....]/<file>.json
		pathParts := strings.Split(parsedURL.Path, "/")
		if len(pathParts) < 5 {
			return nil, fmt.Errorf("invalid URL format using 'raw.'. Expected the form of " +
				"'https://raw.<hostname>/<owner>/<repo>/<branch>/[...path]/<fileName>.json'")
		}
		repoSlug = fmt.Sprintf("%s/%s", pathParts[1], pathParts[2])
		filePath = strings.Join(pathParts[4:], "/")
	} else if strings.HasPrefix(hostname, "api.") {
		// https://api.<hostname>/repos/<owner>/<repo>/contents/[...path]/<file>.json
		pathParts := strings.Split(parsedURL.Path, "/")
		if len(pathParts) < 6 {
			return nil, fmt.Errorf("invalid URL format using 'api.'. Expected the form of " +
				"'https://api.<hostname>/repos/<owner>/<repo>/contents/[...path]/<fileName>.json[?ref=<branch>]'")
		}
		repoSlug = fmt.Sprintf("%s/%s", pathParts[2], pathParts[3])
		filePath = strings.Join(pathParts[5:], "/")
	} else if strings.HasPrefix(urlArg, "https://") {
		// https://<hostname>/<owner>/<repo>/<branch>/[...path]/<file>.json
		pathParts := strings.Split(parsedURL.Path, "/")
		if len(pathParts) < 5 {
			return nil, fmt.Errorf("invalid URL format. Expected the form of " +
				"'https://<hostname>/<owner>/<repo>/<branch>/[...path]/<fileName>.json'")
		}
		repoSlug = fmt.Sprintf("%s/%s", pathParts[1], pathParts[2])
		filePath = strings.Join(pathParts[4:], "/")
	} else {
		return nil, fmt.Errorf(
			"invalid URL format. Expected formats are:\n" +
				"  - 'https://raw.<hostname>/<owner>/<repo>/<branch>/[...path]/<fileName>.json'\n" +
				"  - 'https://<hostname>/<owner>/<repo>/<branch>/[...path]/<fileName>.json'\n" +
				"  - 'https://api.<hostname>/repos/<owner>/<repo>/contents/[...path]/<fileName>.json[?ref=<branch>]'",
		)
	}

	apiPath := fmt.Sprintf("/repos/%s/contents/%s", repoSlug, filePath)
	if hostname == "raw.githubusercontent.com" {
		// raw.githubusercontent.com is for public
		hostname = "github.com"
	}

	authResult, err := ghCli.GetAuthStatus(ctx, hostname)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth status: %w", err)
	}
	if !authResult.LoggedIn {
		// ensure no spinner is shown when logging in, as this is interactive operation
		console.StopSpinner(ctx, "", input.Step)
		err := ghCli.Login(ctx, hostname)
		if err != nil {
			return nil, fmt.Errorf("failed to login: %w", err)
		}
		console.ShowSpinner(ctx, "Validating template source", input.Step)
	}

	content, err := ghCli.ApiCall(ctx, hostname, apiPath, github.ApiCallOptions{
		Headers: []string{"Accept: application/vnd.github.v3.raw"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get content: %w", err)
	}

	return newJsonTemplateSource(name, content)
}
