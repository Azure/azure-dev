// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestBuildOIDCSubject(t *testing.T) {
	repoSlug := "Azure-Samples/my-repo"
	repoInfo := &RepoInfo{
		ID: 599293758,
	}
	repoInfo.Owner.ID = 1844662

	tests := []struct {
		name       string
		repoSlug   string
		repoInfo   *RepoInfo
		oidcConfig *OIDCSubjectConfig
		suffix     string
		want       string
		wantErr    string
	}{
		{
			name:       "nil config uses default format",
			repoSlug:   repoSlug,
			repoInfo:   repoInfo,
			oidcConfig: nil,
			suffix:     "ref:refs/heads/main",
			want:       "repo:Azure-Samples/my-repo:ref:refs/heads/main",
		},
		{
			name:       "use_default true uses default format",
			repoSlug:   repoSlug,
			repoInfo:   repoInfo,
			oidcConfig: &OIDCSubjectConfig{UseDefault: true},
			suffix:     "pull_request",
			want:       "repo:Azure-Samples/my-repo:pull_request",
		},
		{
			name:     "custom owner_id and repo_id claims",
			repoSlug: repoSlug,
			repoInfo: repoInfo,
			oidcConfig: &OIDCSubjectConfig{
				UseDefault: false,
				IncludeClaimKeys: []string{
					"repository_owner_id",
					"repository_id",
				},
			},
			suffix: "ref:refs/heads/main",
			want: "repository_owner_id:1844662:" +
				"repository_id:599293758:ref:refs/heads/main",
		},
		{
			name:     "custom owner_id and repo_id for pull_request",
			repoSlug: repoSlug,
			repoInfo: repoInfo,
			oidcConfig: &OIDCSubjectConfig{
				UseDefault: false,
				IncludeClaimKeys: []string{
					"repository_owner_id",
					"repository_id",
				},
			},
			suffix: "pull_request",
			want: "repository_owner_id:1844662:" +
				"repository_id:599293758:pull_request",
		},
		{
			name:     "custom with repository_owner and repository",
			repoSlug: repoSlug,
			repoInfo: repoInfo,
			oidcConfig: &OIDCSubjectConfig{
				UseDefault: false,
				IncludeClaimKeys: []string{
					"repository_owner",
					"repository",
				},
			},
			suffix: "ref:refs/heads/main",
			want: "repository_owner:Azure-Samples:" +
				"repository:Azure-Samples/my-repo:" +
				"ref:refs/heads/main",
		},
		{
			name:     "empty claim keys with use_default false errors",
			repoSlug: repoSlug,
			repoInfo: repoInfo,
			oidcConfig: &OIDCSubjectConfig{
				UseDefault:       false,
				IncludeClaimKeys: []string{},
			},
			suffix:  "ref:refs/heads/main",
			wantErr: "no claim keys specified",
		},
		{
			name:     "unknown claim key errors",
			repoSlug: repoSlug,
			repoInfo: repoInfo,
			oidcConfig: &OIDCSubjectConfig{
				UseDefault: false,
				IncludeClaimKeys: []string{
					"repository_owner_id",
					"some_future_key",
				},
			},
			suffix:  "ref:refs/heads/main",
			wantErr: "unsupported OIDC claim key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildOIDCSubject(
				tt.repoSlug, tt.repoInfo, tt.oidcConfig, tt.suffix,
			)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGetOIDCSubjectConfig(t *testing.T) {
	repoSlug := "Azure-Samples/my-repo"
	orgName := "Azure-Samples"

	customConfig := OIDCSubjectConfig{
		UseDefault: false,
		IncludeClaimKeys: []string{
			"repository_owner_id", "repository_id",
		},
	}
	customJSON, _ := json.Marshal(customConfig)

	defaultConfig := OIDCSubjectConfig{UseDefault: true}
	defaultJSON, _ := json.Marshal(defaultConfig)

	tests := []struct {
		name     string
		setup    func(mockContext *mocks.MockContext)
		wantConf *OIDCSubjectConfig
		wantErr  string
	}{
		{
			name: "repo-level returns custom config",
			setup: func(mc *mocks.MockContext) {
				mc.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
					return strings.Contains(cmd, "/repos/"+repoSlug+
						"/actions/oidc/customization/sub")
				}).Respond(exec.NewRunResult(
					0, string(customJSON), "",
				))
			},
			wantConf: &customConfig,
		},
		{
			name: "repo-level use_default true is returned as-is",
			setup: func(mc *mocks.MockContext) {
				mc.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
					return strings.Contains(cmd, "/repos/"+repoSlug+
						"/actions/oidc/customization/sub")
				}).Respond(exec.NewRunResult(
					0, string(defaultJSON), "",
				))
			},
			wantConf: &defaultConfig,
		},
		{
			name: "repo 404 falls back to org custom config",
			setup: func(mc *mocks.MockContext) {
				mc.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
					return strings.Contains(cmd, "/repos/"+repoSlug+
						"/actions/oidc/customization/sub")
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(1, "", "HTTP 404: Not Found"),
						fmt.Errorf("HTTP 404: Not Found")
				})
				mc.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
					return strings.Contains(cmd, "/orgs/"+orgName+
						"/actions/oidc/customization/sub")
				}).Respond(exec.NewRunResult(
					0, string(customJSON), "",
				))
			},
			wantConf: &customConfig,
		},
		{
			name: "both 404 returns default",
			setup: func(mc *mocks.MockContext) {
				mc.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
					return strings.Contains(cmd, "/repos/"+repoSlug+
						"/actions/oidc/customization/sub")
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(1, "", "HTTP 404: Not Found"),
						fmt.Errorf("HTTP 404: Not Found")
				})
				mc.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
					return strings.Contains(cmd, "/orgs/"+orgName+
						"/actions/oidc/customization/sub")
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(1, "", "HTTP 404: Not Found"),
						fmt.Errorf("HTTP 404: Not Found")
				})
			},
			wantConf: &OIDCSubjectConfig{UseDefault: true},
		},
		{
			name: "repo non-404 error is returned",
			setup: func(mc *mocks.MockContext) {
				mc.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
					return strings.Contains(cmd, "/repos/"+repoSlug+
						"/actions/oidc/customization/sub")
				}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					return exec.NewRunResult(1, "", "HTTP 403: Forbidden"),
						fmt.Errorf("HTTP 403: Forbidden")
				})
			},
			wantErr: "failed to query repo-level OIDC config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			tt.setup(mockContext)

			cli := NewGitHubCli(
				mockContext.Console, mockContext.CommandRunner,
			)
			// Set path so newRunArgs works
			cli.path = "gh"

			config, err := cli.GetOIDCSubjectConfig(
				t.Context(), repoSlug,
			)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantConf, config)
		})
	}
}

func TestGetRepoInfo(t *testing.T) {
	repoSlug := "Azure-Samples/my-repo"

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(func(args exec.RunArgs, cmd string) bool {
		return strings.Contains(cmd, "/repos/"+repoSlug)
	}).Respond(exec.NewRunResult(
		0, `{"id": 599293758, "owner": {"id": 1844662}}`, "",
	))

	cli := NewGitHubCli(
		mockContext.Console, mockContext.CommandRunner,
	)
	cli.path = "gh"

	info, err := cli.GetRepoInfo(t.Context(), repoSlug)
	require.NoError(t, err)
	require.Equal(t, 599293758, info.ID)
	require.Equal(t, 1844662, info.Owner.ID)
}
