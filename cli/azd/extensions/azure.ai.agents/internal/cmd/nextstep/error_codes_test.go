// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemediationForUserErrorCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code         UserErrorCode
		expectOK     bool
		expectCmdHas string
	}{
		{UserErrorImage, true, "azd ai agent monitor"},
		{UserErrorCodeBlob, true, "azd ai agent monitor"},
		{UserErrorProvisioning, true, "azd ai agent show"},
		{UserErrorCode("UnknownCode"), false, ""},
		{UserErrorCode(""), false, ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			t.Parallel()
			suggestion, ok := RemediationForUserErrorCode(tt.code)
			assert.Equal(t, tt.expectOK, ok)
			if tt.expectOK {
				assert.Contains(t, suggestion.Command, tt.expectCmdHas)
				assert.NotEmpty(t, suggestion.Description)
			} else {
				assert.Equal(t, Suggestion{}, suggestion)
			}
		})
	}
}

func TestRemediationForSessionErrorCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		code             SessionErrorCode
		expectOK         bool
		expectSecondary  bool
		expectPrimaryHas string
	}{
		{"readiness timeout has secondary", SessionReadinessTimeout, true, true, "azd ai agent invoke"},
		{"proxy timeout has no secondary", SessionProxyTimeout, true, false, "azd ai agent monitor"},
		{"sandbox idle retry", SessionSandboxIdle, true, false, "azd ai agent invoke"},
		{"sandbox not found retry", SessionSandboxNotFound, true, false, "azd ai agent invoke"},
		{"quota exceeded lists sessions", SessionQuotaExceeded, true, false, "azd ai agent session list"},
		{"regional quota suggests provision", SessionRegionalQuotaExceeded, true, false, "azd provision"},
		{"agent version not ready polls show", SessionAgentVersionNotReady, true, false, "azd ai agent show"},
		{"version provisioning failed surfaces show", SessionAgentVersionProvisioningFailed, true, false, "azd ai agent show"},
		{"unknown code returns ok=false", SessionErrorCode("Bogus"), false, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			primary, secondary, ok := RemediationForSessionErrorCode(tt.code)
			require.Equal(t, tt.expectOK, ok)
			if !tt.expectOK {
				assert.Equal(t, Suggestion{}, primary)
				assert.Nil(t, secondary)
				return
			}
			assert.Contains(t, primary.Command, tt.expectPrimaryHas)
			assert.NotEmpty(t, primary.Description)
			if tt.expectSecondary {
				require.NotNil(t, secondary)
				assert.NotEmpty(t, secondary.Command)
				assert.NotEmpty(t, secondary.Description)
			} else {
				assert.Nil(t, secondary)
			}
		})
	}
}

// TestErrorCodeWireValues pins the string values to the platform contract.
// Any change here breaks the Foundry hosted-agents service compatibility.
func TestErrorCodeWireValues(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"ImageError":                     string(UserErrorImage),
		"CodeError":                      string(UserErrorCodeBlob),
		"ProvisioningError":              string(UserErrorProvisioning),
		"ReadinessTimeout":               string(SessionReadinessTimeout),
		"ProxyTimeout":                   string(SessionProxyTimeout),
		"SandboxIdle":                    string(SessionSandboxIdle),
		"SandboxNotFound":                string(SessionSandboxNotFound),
		"QuotaExceeded":                  string(SessionQuotaExceeded),
		"RegionalQuotaExceeded":          string(SessionRegionalQuotaExceeded),
		"AgentVersionNotReady":           string(SessionAgentVersionNotReady),
		"AgentVersionProvisioningFailed": string(SessionAgentVersionProvisioningFailed),
		"creating":                       string(AgentVersionCreating),
		"active":                         string(AgentVersionActive),
		"idle":                           string(AgentVersionIdle),
		"failed":                         string(AgentVersionFailed),
		"deleting":                       string(AgentVersionDeleting),
		"deleted":                        string(AgentVersionDeleted),
	}

	for expected, actual := range cases {
		assert.Equal(t, expected, actual, "wire value drift")
	}
}
