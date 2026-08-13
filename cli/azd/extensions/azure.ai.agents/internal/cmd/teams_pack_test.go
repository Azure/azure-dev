// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestResolveTeamsPackScope(t *testing.T) {
	cases := []struct {
		in      string
		wantAPI string
	}{
		{"", "Personal"},
		{"personal", "Personal"},
		{"PERSONAL", "Personal"},
		{"  shared  ", "Shared"},
		{"org", "Tenant"},
		{"tenant", "Tenant"},
	}
	for _, c := range cases {
		got, err := resolveTeamsPackScope(c.in)
		if err != nil {
			t.Fatalf("resolveTeamsPackScope(%q) returned error: %v", c.in, err)
		}
		if got.api != c.wantAPI {
			t.Errorf("resolveTeamsPackScope(%q).api = %q, want %q", c.in, got.api, c.wantAPI)
		}
	}
}

func TestResolveTeamsPackScopeInvalid(t *testing.T) {
	_, err := resolveTeamsPackScope("everyone")
	if err == nil {
		t.Fatal("expected error for unsupported scope, got nil")
	}
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected *azdext.LocalError, got %T", err)
	}
	if localErr.Code != exterrors.CodeInvalidPublishScope {
		t.Errorf("error code = %q, want %q", localErr.Code, exterrors.CodeInvalidPublishScope)
	}
}

func TestBuildTeamsAppPackageRequest(t *testing.T) {
	scope, err := resolveTeamsPackScope("shared")
	if err != nil {
		t.Fatal(err)
	}
	req := buildTeamsAppPackageRequest("/subscriptions/s/bot", teamsAppRequestOptions{
		scope:       scope,
		displayName: "Contoso Helper",
		appVersion:  "",
	})
	if req.BotServiceArmID != "/subscriptions/s/bot" {
		t.Errorf("BotServiceArmID = %q", req.BotServiceArmID)
	}
	if req.PublishScope != "Shared" {
		t.Errorf("PublishScope = %q, want Shared", req.PublishScope)
	}
	if req.AgentDisplayName != "Contoso Helper" {
		t.Errorf("AgentDisplayName = %q", req.AgentDisplayName)
	}
	if req.AppVersion != "1.0.0" {
		t.Errorf("AppVersion = %q, want default 1.0.0", req.AppVersion)
	}
	if !req.CanRespondWithoutMention {
		t.Error("CanRespondWithoutMention = false, want true")
	}
}

func TestTeamsBotArmIDUsesPersistedBotTarget(t *testing.T) {
	got, err := teamsBotArmID("sub-1", "default-rg", "adopted-bot", "published-rg")
	if err != nil {
		t.Fatal(err)
	}

	want := "/subscriptions/sub-1/resourceGroups/published-rg/providers/Microsoft.BotService/botServices/adopted-bot"
	if got != want {
		t.Errorf("teamsBotArmID = %q, want %q", got, want)
	}
}

func TestTeamsBotArmIDFallsBackToEnvironmentResourceGroup(t *testing.T) {
	got, err := teamsBotArmID("sub-1", "default-rg", "owned-bot", "")
	if err != nil {
		t.Fatal(err)
	}

	want := "/subscriptions/sub-1/resourceGroups/default-rg/providers/Microsoft.BotService/botServices/owned-bot"
	if got != want {
		t.Errorf("teamsBotArmID = %q, want %q", got, want)
	}
}

func TestTeamsBotArmIDRequiresPersistedBotName(t *testing.T) {
	_, err := teamsBotArmID("sub-1", "default-rg", " ", "published-rg")
	if err == nil {
		t.Fatal("expected missing bot name error")
	}
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected *azdext.LocalError, got %T", err)
	}
	if localErr.Code != exterrors.CodeAgentNotDeployed {
		t.Errorf("error code = %q, want %q", localErr.Code, exterrors.CodeAgentNotDeployed)
	}
}

func TestTeamsAppDeepLink(t *testing.T) {
	got := teamsAppDeepLink("T_abc 123")
	want := "https://teams.microsoft.com/v2/#/l/app/?source=agent-details-page&titleId=T_abc+123&launchAgent=join_launcher_web"
	if got != want {
		t.Errorf("teamsAppDeepLink = %q, want %q", got, want)
	}
}

func TestValidatePublishScope(t *testing.T) {
	personal, _ := resolveTeamsPackScope("personal")
	if err := validatePublishScope(personal); err == nil {
		t.Error("expected personal scope to be rejected for publish, got nil")
	}
	for _, s := range []string{"shared", "tenant", "org"} {
		scope, _ := resolveTeamsPackScope(s)
		if err := validatePublishScope(scope); err != nil {
			t.Errorf("validatePublishScope(%q) = %v, want nil", s, err)
		}
	}
}
