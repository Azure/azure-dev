// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	maxRepositoryMetadataSize = 1 << 20
	repositoryMetadataTimeout = 5 * time.Second
)

// RepositoryStatus contains hosting-provider metadata relevant to template initialization.
type RepositoryStatus struct {
	Archived bool
}

// RepositoryStatusChecker retrieves repository metadata when the hosting provider supports it.
type RepositoryStatusChecker interface {
	// Check returns nil when the repository host is not supported.
	Check(ctx context.Context, repositoryURL string) (*RepositoryStatus, error)
}

type githubRepositoryStatusChecker struct {
	transport      policy.Transporter
	hosts          map[string]string
	requestTimeout time.Duration
}

// NewGitHubRepositoryStatusChecker creates a checker for GitHub-hosted repositories.
func NewGitHubRepositoryStatusChecker(transport policy.Transporter) RepositoryStatusChecker {
	if transport == nil {
		transport = http.DefaultClient
	}

	hosts := map[string]string{
		"github.com": firstSetEnvironmentVariable("GH_TOKEN", "GITHUB_TOKEN"),
	}
	enterpriseToken := firstSetEnvironmentVariable("GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN")
	if host := normalizeGitHubHost(os.Getenv("GH_HOST")); host != "" && host != "github.com" {
		hosts[host] = enterpriseToken
	}
	if serverURL := os.Getenv("GITHUB_SERVER_URL"); serverURL != "" {
		if parsed, err := url.Parse(serverURL); err == nil {
			if host := normalizeGitHubHost(parsed.Hostname()); host != "" && host != "github.com" {
				hosts[host] = enterpriseToken
			}
		}
	}

	return &githubRepositoryStatusChecker{
		transport:      transport,
		hosts:          hosts,
		requestTimeout: repositoryMetadataTimeout,
	}
}

func (c *githubRepositoryStatusChecker) Check(
	ctx context.Context,
	repositoryURL string,
) (*RepositoryStatus, error) {
	host, slug, ok := parseGitHubRepositoryURL(repositoryURL, c.hosts)
	if !ok {
		return nil, nil
	}

	apiURL := fmt.Sprintf("https://%s/api/v3/repos/%s", host, slug)
	if host == "github.com" {
		apiURL = fmt.Sprintf("https://api.github.com/repos/%s", slug)
	}

	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating GitHub repository metadata request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "azd")
	if token := c.hosts[host]; token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.transport.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting GitHub repository metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("requesting GitHub repository metadata: HTTP %d", resp.StatusCode)
	}

	var metadata struct {
		Archived bool `json:"archived"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRepositoryMetadataSize)).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("decoding GitHub repository metadata: %w", err)
	}

	return &RepositoryStatus{Archived: metadata.Archived}, nil
}

func parseGitHubRepositoryURL(
	repositoryURL string,
	knownHosts map[string]string,
) (host string, slug string, ok bool) {
	var path string

	if after, ok0 := strings.CutPrefix(repositoryURL, "git@"); ok0 {
		hostAndPath := after
		host, path, ok = strings.Cut(hostAndPath, ":")
		if !ok {
			return "", "", false
		}
	} else {
		parsed, err := url.Parse(repositoryURL)
		if err != nil {
			return "", "", false
		}

		switch parsed.Scheme {
		case "http", "https", "ssh", "git":
		default:
			return "", "", false
		}

		host = parsed.Hostname()
		path = parsed.Path
	}

	host = normalizeGitHubHost(host)
	if _, known := knownHosts[host]; !known {
		return "", "", false
	}

	path = strings.Trim(strings.TrimSpace(path), "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}

	return host, url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), true
}

func normalizeGitHubHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.TrimPrefix(host, "www.")
}

func firstSetEnvironmentVariable(names ...string) string {
	for _, name := range names {
		if token := os.Getenv(name); token != "" {
			return token
		}
	}

	return ""
}
