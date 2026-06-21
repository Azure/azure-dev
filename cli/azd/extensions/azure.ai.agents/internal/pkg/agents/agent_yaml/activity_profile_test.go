// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import "testing"

func activityAgent(useCase string, protocols ...string) ContainerAgent {
	recs := make([]ProtocolVersionRecord, 0, len(protocols))
	for _, p := range protocols {
		recs = append(recs, ProtocolVersionRecord{Protocol: p, Version: "1.0.0"})
	}
	c := ContainerAgent{Protocols: recs}
	if useCase != "" {
		c.Activity = &ActivityConfig{UseCase: useCase}
	}
	return c
}

func TestContainerAgent_IsActivityProtocol(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		protocols []string
		want      bool
	}{
		{"friendly activity", []string{"activity"}, true},
		{"wire activity_protocol", []string{"activity_protocol"}, true},
		{"mixed case + spaces", []string{"  Activity_Protocol "}, true},
		{"responses only", []string{"responses"}, false},
		{"none", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := activityAgent("", tc.protocols...).IsActivityProtocol()
			if got != tc.want {
				t.Errorf("IsActivityProtocol() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContainerAgent_ResolveActivityProfile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		useCase         string
		protocols       []string
		hasBlueprintEnv bool
		wantIsActivity  bool
		wantUseCase     ActivityUseCase
		wantEndpoint    bool
		wantRbac        bool
		wantBlueprint   bool
		wantProvision   bool
		wantPublish     bool
	}{
		{
			name:           "non-activity agent",
			protocols:      []string{"responses"},
			wantIsActivity: false,
		},
		{
			name:           "explicit digital_worker",
			useCase:        "digital_worker",
			protocols:      []string{"activity"},
			wantIsActivity: true,
			wantUseCase:    ActivityUseCaseDigitalWorker,
			wantEndpoint:   true,
			wantRbac:       true,
			wantBlueprint:  true,
			wantProvision:  true,
			wantPublish:    true,
		},
		{
			name:           "explicit simple",
			useCase:        "simple",
			protocols:      []string{"activity"},
			wantIsActivity: true,
			wantUseCase:    ActivityUseCaseSimple,
			wantEndpoint:   true,
			// no rbac/blueprint/m365
		},
		{
			name:            "explicit simple wins over blueprint env",
			useCase:         "simple",
			protocols:       []string{"activity"},
			hasBlueprintEnv: true,
			wantIsActivity:  true,
			wantUseCase:     ActivityUseCaseSimple,
			wantEndpoint:    true,
		},
		{
			name:            "unset falls back to digital_worker when blueprint env present",
			protocols:       []string{"activity"},
			hasBlueprintEnv: true,
			wantIsActivity:  true,
			wantUseCase:     ActivityUseCaseDigitalWorker,
			wantEndpoint:    true,
			wantRbac:        true,
			wantBlueprint:   true,
			wantProvision:   true,
			wantPublish:     true,
		},
		{
			name:           "unset falls back to simple without blueprint env",
			protocols:      []string{"activity"},
			wantIsActivity: true,
			wantUseCase:    ActivityUseCaseSimple,
			wantEndpoint:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := activityAgent(tc.useCase, tc.protocols...).ResolveActivityProfile(tc.hasBlueprintEnv)
			if p.IsActivity != tc.wantIsActivity {
				t.Fatalf("IsActivity = %v, want %v", p.IsActivity, tc.wantIsActivity)
			}
			if !tc.wantIsActivity {
				return
			}
			if p.UseCase != tc.wantUseCase {
				t.Errorf("UseCase = %q, want %q", p.UseCase, tc.wantUseCase)
			}
			if p.InjectActivityEndpoint != tc.wantEndpoint {
				t.Errorf("InjectActivityEndpoint = %v, want %v", p.InjectActivityEndpoint, tc.wantEndpoint)
			}
			if p.InjectBotServiceRbac != tc.wantRbac {
				t.Errorf("InjectBotServiceRbac = %v, want %v", p.InjectBotServiceRbac, tc.wantRbac)
			}
			if p.InjectBlueprintReference != tc.wantBlueprint {
				t.Errorf("InjectBlueprintReference = %v, want %v", p.InjectBlueprintReference, tc.wantBlueprint)
			}
			if p.M365Provision != tc.wantProvision {
				t.Errorf("M365Provision = %v, want %v", p.M365Provision, tc.wantProvision)
			}
			if p.M365Publish != tc.wantPublish {
				t.Errorf("M365Publish = %v, want %v", p.M365Publish, tc.wantPublish)
			}
		})
	}
}
