// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAgentIdentityInfo(t *testing.T) {
	tests := []struct {
		name        string
		resourceID  string
		wantAccount string
		wantProject string
		wantSubID   string
		wantRG      string
		wantScope   string
		wantErr     bool
	}{
		{
			name: "valid resource ID",
			resourceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
			wantAccount: "my-account",
			wantProject: "my-project",
			wantSubID:   "sub-123",
			wantRG:      "rg-test",
			wantScope: "/subscriptions/sub-123/resourceGroups/rg-test/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account",
			wantErr: false,
		},
		{
			name: "resource ID with extra segments",
			resourceID: "/subscriptions/aaaa-bbbb/resourceGroups/my-rg/providers/" +
				"Microsoft.CognitiveServices/accounts/acct-name/projects/proj-name/extraSegment/value",
			wantAccount: "acct-name",
			wantProject: "proj-name",
			wantSubID:   "aaaa-bbbb",
			wantRG:      "my-rg",
			wantScope: "/subscriptions/aaaa-bbbb/resourceGroups/my-rg/providers/" +
				"Microsoft.CognitiveServices/accounts/acct-name",
			wantErr: false,
		},
		{
			name:       "too short resource ID",
			resourceID: "/subscriptions/sub/resourceGroups/rg",
			wantErr:    true,
		},
		{
			name:       "empty string",
			resourceID: "",
			wantErr:    true,
		},
		{
			name: "missing project segment",
			resourceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseAgentIdentityInfo(tt.resourceID)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantAccount, info.AccountName)
			assert.Equal(t, tt.wantProject, info.ProjectName)
			assert.Equal(t, tt.wantSubID, info.SubscriptionID)
			assert.Equal(t, tt.wantRG, info.ResourceGroup)
			assert.Equal(t, tt.wantScope, info.AccountScope)
		})
	}
}

func TestAgentIdentityDisplayName(t *testing.T) {
	tests := []struct {
		account string
		project string
		want    string
	}{
		{"my-account", "my-project", "my-account-my-project-AgentIdentity"},
		{"acct", "proj", "acct-proj-AgentIdentity"},
		{"a-b-c", "x-y-z", "a-b-c-x-y-z-AgentIdentity"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := agentIdentityDisplayName(tt.account, tt.project)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractSubscriptionID(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		want       string
	}{
		{
			name: "valid resource ID",
			resourceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account",
			want: "sub-123",
		},
		{
			name:       "no subscription segment",
			resourceID: "/resourceGroups/rg-test/providers/Microsoft.CognitiveServices/accounts/my-account",
			want:       "",
		},
		{
			name:       "empty string",
			resourceID: "",
			want:       "",
		},
		{
			name:       "subscription at end with no value",
			resourceID: "/subscriptions/",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSubscriptionID(tt.resourceID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsVnextEnabled(t *testing.T) {
	tests := []struct {
		name      string
		azdEnv    map[string]string
		osEnv     string
		setOsEnv  bool
		want      bool
	}{
		{
			name:   "enabled via azd env true",
			azdEnv: map[string]string{"enableHostedAgentVNext": "true"},
			want:   true,
		},
		{
			name:   "enabled via azd env 1",
			azdEnv: map[string]string{"enableHostedAgentVNext": "1"},
			want:   true,
		},
		{
			name:   "disabled via azd env false",
			azdEnv: map[string]string{"enableHostedAgentVNext": "false"},
			want:   false,
		},
		{
			name:   "not set in azd env",
			azdEnv: map[string]string{},
			want:   false,
		},
		{
			name:     "fallback to os env",
			azdEnv:   map[string]string{},
			osEnv:    "true",
			setOsEnv: true,
			want:     true,
		},
		{
			name:   "invalid value",
			azdEnv: map[string]string{"enableHostedAgentVNext": "notabool"},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setOsEnv {
				t.Setenv("enableHostedAgentVNext", tt.osEnv)
			} else {
				t.Setenv("enableHostedAgentVNext", "")
			}

			got := isVnextEnabled(tt.azdEnv)
			assert.Equal(t, tt.want, got)
		})
	}
}
