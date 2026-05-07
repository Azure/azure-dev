// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSlugForRemote_AdditionalCases(t *testing.T) {
	tests := []struct {
		name    string
		remote  string
		want    string
		wantErr bool
	}{
		{
			name:   "SSHWithHyphensInOrgAndRepo",
			remote: "git@github.com:my-org/my-repo.git",
			want:   "my-org/my-repo",
		},
		{
			name:   "SSHWithUnderscoresInRepo",
			remote: "git@github.com:org/my_repo.git",
			want:   "org/my_repo",
		},
		{
			name:   "HTTPSWithDotsInRepoName",
			remote: "https://github.com/org/repo.v2.git",
			want:   "org/repo.v2",
		},
		{
			name:   "SSHWithDotsNoGitSuffix",
			remote: "git@github.com:org/repo.v2",
			want:   "org/repo.v2",
		},
		{
			name:   "HTTPSNestedPath",
			remote: "https://github.com/org/repo/sub/path",
			want:   "org/repo/sub/path",
		},
		{
			name:   "SSHNestedPath",
			remote: "git@github.com:org/repo/sub/path",
			want:   "org/repo/sub/path",
		},
		{
			name:    "GitLabSSH",
			remote:  "git@gitlab.com:org/repo.git",
			wantErr: true,
		},
		{
			name:    "BitbucketHTTPS",
			remote:  "https://bitbucket.org/org/repo.git",
			wantErr: true,
		},
		{
			name:    "AzureDevOpsSSH",
			remote:  "git@ssh.dev.azure.com:v3/org/proj/repo",
			wantErr: true,
		},
		{
			name:    "HTTPNotHTTPS",
			remote:  "http://github.com/org/repo.git",
			wantErr: true,
		},
		{
			name:    "GitHubEnterpriseHTTPS",
			remote:  "https://github.example.com/org/repo.git",
			wantErr: true,
		},
		{
			name:   "TrailingSlashReturnsEmptySlug",
			remote: "https://github.com/",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, err := GetSlugForRemote(tt.remote)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(
					t, err, ErrRemoteHostIsNotGitHub,
				)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, slug)
		})
	}
}

func TestErrRemoteHostIsNotGitHub_SentinelError(t *testing.T) {
	// Verify the sentinel error works with errors.Is
	_, err := GetSlugForRemote("https://not-github.com/o/r")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRemoteHostIsNotGitHub))

	// Verify the error message is meaningful
	assert.Equal(
		t,
		"not a github host",
		ErrRemoteHostIsNotGitHub.Error(),
	)
}

func TestGetSlugForRemote_WWWVariant(t *testing.T) {
	// www.github.com should work the same as github.com
	tests := []struct {
		name   string
		remote string
		want   string
	}{
		{
			name:   "WWWWithGitSuffix",
			remote: "https://www.github.com/org/repo.git",
			want:   "org/repo",
		},
		{
			name:   "WWWWithoutGitSuffix",
			remote: "https://www.github.com/org/repo",
			want:   "org/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug, err := GetSlugForRemote(tt.remote)
			require.NoError(t, err)
			assert.Equal(t, tt.want, slug)
		})
	}
}
