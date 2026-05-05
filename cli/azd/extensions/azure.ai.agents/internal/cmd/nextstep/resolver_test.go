// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"strings"
	"testing"
)

func TestResolveAfterInvokeLocal(t *testing.T) {
	tests := []struct {
		name           string
		state          *State
		wantHasDeploy  bool
		wantHasMonitor bool
	}{
		{
			name:           "nil state -> nothing",
			state:          nil,
			wantHasDeploy:  false,
			wantHasMonitor: false,
		},
		{
			name: "no project endpoint -> deploy only",
			state: &State{
				AgentServices: []ServiceState{{ServiceName: "calculator"}},
			},
			wantHasDeploy:  true,
			wantHasMonitor: false,
		},
		{
			name: "single agent + endpoint -> deploy + monitor",
			state: &State{
				HasProjectEndpoint: true,
				AgentServices:      []ServiceState{{ServiceName: "calculator"}},
			},
			wantHasDeploy:  true,
			wantHasMonitor: true,
		},
		{
			name: "multi-agent + endpoint -> deploy only (monitor is per-agent post-deploy)",
			state: &State{
				HasProjectEndpoint: true,
				AgentServices: []ServiceState{
					{ServiceName: "a"},
					{ServiceName: "b"},
				},
			},
			wantHasDeploy:  true,
			wantHasMonitor: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveAfterInvokeLocal(tc.state)
			gotDeploy := containsCommand(got, "azd deploy")
			gotMonitor := containsCommand(got, "azd ai agent monitor --follow")
			if gotDeploy != tc.wantHasDeploy {
				t.Errorf("deploy: got=%v want=%v", gotDeploy, tc.wantHasDeploy)
			}
			if gotMonitor != tc.wantHasMonitor {
				t.Errorf("monitor: got=%v want=%v", gotMonitor, tc.wantHasMonitor)
			}
		})
	}
}

func TestResolveAfterInvokeRemote(t *testing.T) {
	t.Run("with agent name embeds it in show", func(t *testing.T) {
		got := ResolveAfterInvokeRemote(&State{HasProjectEndpoint: true}, "calculator-v1")
		if !containsCommand(got, "azd ai agent show calculator-v1") {
			t.Errorf("expected show with agent name, got %#v", got)
		}
		if !containsCommand(got, "azd ai agent monitor --follow") {
			t.Errorf("expected monitor when project endpoint set, got %#v", got)
		}
	})
	t.Run("empty agent name falls back to bare show", func(t *testing.T) {
		got := ResolveAfterInvokeRemote(&State{}, "")
		if !containsCommand(got, "azd ai agent show") {
			t.Errorf("expected bare show, got %#v", got)
		}
	})
	t.Run("no project endpoint omits monitor", func(t *testing.T) {
		got := ResolveAfterInvokeRemote(&State{}, "calc")
		if containsCommand(got, "azd ai agent monitor --follow") {
			t.Errorf("did not expect monitor without endpoint, got %#v", got)
		}
	})
}

func TestResolveAfterShow(t *testing.T) {
	t.Run("active -> invoke + monitor", func(t *testing.T) {
		got := ResolveAfterShow(&State{HasProjectEndpoint: true}, "calc", "active")
		if !containsSuffix(got, "azd ai agent invoke calc \"Hello!\"") {
			t.Errorf("expected invoke <agent>, got %#v", got)
		}
		if !containsCommand(got, "azd ai agent monitor --follow") {
			t.Errorf("expected monitor, got %#v", got)
		}
	})
	t.Run("failed -> monitor only", func(t *testing.T) {
		got := ResolveAfterShow(nil, "calc", "Failed")
		if !containsCommand(got, "azd ai agent monitor --follow") {
			t.Errorf("expected monitor for failed, got %#v", got)
		}
		if containsSuffix(got, "invoke") {
			t.Errorf("did not expect invoke for failed status, got %#v", got)
		}
	})
	t.Run("deploying -> retry show", func(t *testing.T) {
		got := ResolveAfterShow(nil, "calc", "deploying")
		if !containsCommand(got, "azd ai agent show calc") {
			t.Errorf("expected retry show for deploying, got %#v", got)
		}
	})
	t.Run("empty status -> happy path", func(t *testing.T) {
		got := ResolveAfterShow(&State{HasProjectEndpoint: true}, "calc", "")
		if !containsSuffix(got, "azd ai agent invoke calc \"Hello!\"") {
			t.Errorf("expected invoke for empty status, got %#v", got)
		}
	})
}

func TestResolveAfterDeploy(t *testing.T) {
	t.Run("nil state -> nothing", func(t *testing.T) {
		if got := ResolveAfterDeploy(nil); len(got) != 0 {
			t.Errorf("expected empty, got %#v", got)
		}
	})
	t.Run("single agent uses deployed name", func(t *testing.T) {
		got := ResolveAfterDeploy(&State{AgentServices: []ServiceState{
			{ServiceName: "svc", DeployedName: "calc-v3", IsDeployed: true},
		}})
		if !containsCommand(got, "azd ai agent show calc-v3") {
			t.Errorf("expected show with deployed name, got %#v", got)
		}
		if !containsSuffix(got, "azd ai agent invoke calc-v3 \"Hello!\"") {
			t.Errorf("expected invoke, got %#v", got)
		}
	})
	t.Run("multi-agent emits one show per service", func(t *testing.T) {
		got := ResolveAfterDeploy(&State{AgentServices: []ServiceState{
			{ServiceName: "a", DeployedName: "a-v1"},
			{ServiceName: "b", DeployedName: "b-v1"},
		}})
		if !containsCommand(got, "azd ai agent show a-v1") || !containsCommand(got, "azd ai agent show b-v1") {
			t.Errorf("expected one show per deployed agent, got %#v", got)
		}
	})
	t.Run("single agent without deployed name falls back to service name", func(t *testing.T) {
		got := ResolveAfterDeploy(&State{AgentServices: []ServiceState{
			{ServiceName: "calculator"},
		}})
		if !containsCommand(got, "azd ai agent show calculator") {
			t.Errorf("expected service-name fallback, got %#v", got)
		}
	})
}

// containsCommand reports whether suggestions contain an entry whose
// Command starts with prefix.
func containsCommand(suggestions []Suggestion, prefix string) bool {
	for _, s := range suggestions {
		if strings.HasPrefix(s.Command, prefix) {
			return true
		}
	}
	return false
}

// containsSuffix reports whether any suggestion's Command contains needle.
func containsSuffix(suggestions []Suggestion, needle string) bool {
	for _, s := range suggestions {
		if strings.Contains(s.Command, needle) {
			return true
		}
	}
	return false
}
