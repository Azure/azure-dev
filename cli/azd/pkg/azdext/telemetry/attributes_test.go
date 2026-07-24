// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import "testing"

// TestContractValues locks the public wire values. Downstream telemetry
// pipelines and the host allowlist depend on these exact strings, so a change
// here is a breaking telemetry contract change and must be intentional.
func TestContractValues(t *testing.T) {
	t.Parallel()

	if AgentDeploymentModeAttribute != "agent.deploy.mode" {
		t.Fatalf("unexpected attribute key: %q", AgentDeploymentModeAttribute)
	}

	cases := map[AgentDeploymentMode]string{
		AgentDeploymentModeCode:      "code",
		AgentDeploymentModeContainer: "container",
		AgentDeploymentModeByoImage:  "byo_image",
	}
	for mode, want := range cases {
		if string(mode) != want {
			t.Errorf("unexpected mode value: got %q, want %q", string(mode), want)
		}
	}
}
