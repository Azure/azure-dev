// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// OIDCSubjectConfig represents the OIDC subject claim customization
// returned by the GitHub Actions OIDC customization API.
type OIDCSubjectConfig struct {
	UseDefault       bool     `json:"use_default"`
	IncludeClaimKeys []string `json:"include_claim_keys"`
}

// RepoInfo holds GitHub API repository metadata needed for OIDC subject construction.
type RepoInfo struct {
	ID    int64 `json:"id"`
	Owner struct {
		ID int64 `json:"id"`
	} `json:"owner"`
}

// isGitHubNotFoundError returns true if the error indicates a GitHub 404/not-found response.
func isGitHubNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "404") || strings.Contains(errText, "not found")
}

// GetOIDCSubjectConfig queries the GitHub OIDC customization API for a repository.
// It first checks the repo-level customization. If the repo returns a valid response
// (even with UseDefault=true), it is returned as-is — this handles the case where a repo
// explicitly sets use_default=true to override an org-level customization.
// Only when the repo-level endpoint returns 404 does it fall back to the org-level endpoint.
// If both return 404, it returns a config with UseDefault=true (the default format).
func (cli *Cli) GetOIDCSubjectConfig(
	ctx context.Context, repoSlug string,
) (*OIDCSubjectConfig, error) {
	// Try repo-level first.
	runArgs := cli.newRunArgs(
		"api", "/repos/"+repoSlug+"/actions/oidc/customization/sub",
	)
	res, err := cli.run(ctx, runArgs)
	if err == nil {
		var config OIDCSubjectConfig
		if jsonErr := json.Unmarshal([]byte(res.Stdout), &config); jsonErr != nil {
			return nil, fmt.Errorf(
				"failed to parse OIDC config for %s: %w", repoSlug, jsonErr,
			)
		}
		return &config, nil
	}

	if !isGitHubNotFoundError(err) {
		return nil, fmt.Errorf(
			"failed to query repo-level OIDC config for %s: %w", repoSlug, err,
		)
	}

	// Fall back to org-level only when the repo-level endpoint returns 404.
	parts := strings.SplitN(repoSlug, "/", 2)
	if len(parts) == 2 {
		orgRunArgs := cli.newRunArgs(
			"api",
			"/orgs/"+parts[0]+"/actions/oidc/customization/sub",
		)
		orgRes, orgErr := cli.run(ctx, orgRunArgs)
		if orgErr == nil {
			var config OIDCSubjectConfig
			if jsonErr := json.Unmarshal(
				[]byte(orgRes.Stdout), &config,
			); jsonErr != nil {
				return nil, fmt.Errorf(
					"failed to parse org OIDC config for %s: %w",
					parts[0], jsonErr,
				)
			}
			return &config, nil
		}

		if !isGitHubNotFoundError(orgErr) {
			return nil, fmt.Errorf(
				"failed to query org-level OIDC config for %s: %w",
				parts[0], orgErr,
			)
		}
	}

	// Default: no customization.
	return &OIDCSubjectConfig{UseDefault: true}, nil
}

// GetRepoInfo queries the GitHub API for repository metadata (IDs) needed to
// construct OIDC subjects with custom claim keys like repository_owner_id.
func (cli *Cli) GetRepoInfo(
	ctx context.Context, repoSlug string,
) (*RepoInfo, error) {
	runArgs := cli.newRunArgs(
		"api", "/repos/"+repoSlug,
		"--jq", "{id: .id, owner: {id: .owner.id}}",
	)
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get repository info for %s: %w", repoSlug, err,
		)
	}

	var info RepoInfo
	if err := json.Unmarshal([]byte(res.Stdout), &info); err != nil {
		return nil, fmt.Errorf(
			"failed to parse repository info for %s: %w", repoSlug, err,
		)
	}
	return &info, nil
}

// BuildOIDCSubject constructs the correct OIDC subject claim string for a
// federated identity credential based on the OIDC customization config.
//
// This is a pure function — all needed data (repo info, config) must be
// pre-fetched. The suffix is the trailing part of the subject, e.g.
// "ref:refs/heads/main" or "pull_request".
//
// If oidcConfig is nil or UseDefault is true, the default GitHub format is used:
//
//	repo:{owner}/{repo}:{suffix}
//
// For custom configs, claim keys are mapped to values and joined with the
// suffix. Unknown claim keys cause an error so the user knows azd needs
// updating.
func BuildOIDCSubject(
	repoSlug string,
	repoInfo *RepoInfo,
	oidcConfig *OIDCSubjectConfig,
	suffix string,
) (string, error) {
	if oidcConfig == nil || oidcConfig.UseDefault {
		return fmt.Sprintf("repo:%s:%s", repoSlug, suffix), nil
	}

	if len(oidcConfig.IncludeClaimKeys) == 0 {
		return "", fmt.Errorf(
			"OIDC config for %s has use_default=false but no"+
				" claim keys specified", repoSlug,
		)
	}

	var parts []string
	for _, key := range oidcConfig.IncludeClaimKeys {
		switch key {
		case "repository_owner_id":
			if repoInfo == nil {
				return "", fmt.Errorf(
					"OIDC config for %s includes claim key %q"+
						" but repository metadata is required",
					repoSlug, key,
				)
			}
			parts = append(parts,
				fmt.Sprintf("repository_owner_id:%d", repoInfo.Owner.ID),
			)
		case "repository_id":
			if repoInfo == nil {
				return "", fmt.Errorf(
					"OIDC config for %s includes claim key %q"+
						" but repository metadata is required",
					repoSlug, key,
				)
			}
			parts = append(parts,
				fmt.Sprintf("repository_id:%d", repoInfo.ID),
			)
		case "repository_owner":
			owner := strings.SplitN(repoSlug, "/", 2)
			parts = append(parts,
				fmt.Sprintf("repository_owner:%s", owner[0]),
			)
		case "repository":
			parts = append(parts,
				fmt.Sprintf("repository:%s", repoSlug),
			)
		default:
			return "", fmt.Errorf(
				"unsupported OIDC claim key %q in subject"+
					" template for %s — azd may need to be updated",
				key, repoSlug,
			)
		}
	}
	parts = append(parts, suffix)
	return strings.Join(parts, ":"), nil
}
