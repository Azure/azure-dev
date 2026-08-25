// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package useragent

import (
	"testing"

	"azureaiagent/internal/version"

	"github.com/stretchr/testify/require"
)

func TestDefault(t *testing.T) {
	tests := []struct {
		name         string
		environment  string
		wantSkillTag bool
	}{
		{name: "no marker"},
		{name: "unrelated value", environment: "other-client/1.0"},
		{name: "skill marker", environment: foundrySkillEnvValue, wantSkillTag: true},
		{
			name:         "skill marker with another value",
			environment:  "azure_app_space_portal:v1.0.0 " + foundrySkillEnvValue,
			wantSkillTag: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(azureDevUserAgentEnv, test.environment)

			want := defaultUserAgent + "/" + version.Version
			if test.wantSkillTag {
				want += "," + foundrySkillUserAgent
			}

			require.Equal(t, want, Default())
		})
	}
}

func TestValuesDoNotForwardEnvironmentValue(t *testing.T) {
	t.Setenv(azureDevUserAgentEnv, "other-client/1.0 "+foundrySkillEnvValue)

	require.Equal(t, connectionUserAgent+","+foundrySkillUserAgent, Connection())
	require.Equal(t, doctorUserAgent+","+foundrySkillUserAgent, Doctor())
}
