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

// isGitHubNotFoundError returns true if the error indicates a GitHub HTTP 404 response.
func isGitHubNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	errText := strings.ToLower(err.Error())

	// Only treat explicit HTTP 404 signals as GitHub "not found" responses.
	// Avoid matching generic "not found" text, which can appear in unrelated
	// failures such as missing executables, network issues, or permission errors.
	return strings.Contains(errText, "http 404") ||
		strings.Contains(errText, "404 not found")
}

// GetOIDCSubjectConfig queries the GitHub OIDC customization API for a repository.
// It checks the repo-level customization endpoint. If the repo returns a valid response
// (even with UseDefault=true), it is returned as-is.
//
// A repo-level 404 means the repository has not opted in to custom OIDC subjects.
// Per GitHub's docs, repos that haven't opted in receive default-format tokens
// regardless of any org-level template, so we return the default config directly
// without checking the org endpoint.
func (cli *Cli) GetOIDCSubjectConfig(
	ctx context.Context, repoSlug string,
) (*OIDCSubjectConfig, error) {
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

	// Repo 404 = not opted in → default format.
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
			// repoSlug is always "owner/repo" — constructed by the caller
			// from gitRepositoryDetails.owner + "/" + repoDetails.repoName.
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
