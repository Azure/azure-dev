// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

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
		scope:      scope,
		agentName:  "Contoso Helper",
		appVersion: "",
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

func TestBuildTeamsAppPackageRequest_DigitalWorkerUsesPublishMetadata(t *testing.T) {
	scope, err := resolveTeamsPackScope("shared")
	if err != nil {
		t.Fatal(err)
	}
	canRespond := true
	publish := &project.DigitalWorkerPublishConfig{
		PublishAsAutopilot:       true,
		PublishScope:             "tenant",
		CanRespondWithoutMention: &canRespond,
		AppVersion:               "2.3.4",
		AgentDisplayName:         "DW Helper",
		ShortDescription:         "A digital worker",
		FullDescription:          "A digital worker for Teams",
		DeveloperName:            "Contoso",
		DeveloperWebsiteURL:      "https://contoso.example",
		PrivacyURL:               "https://contoso.example/privacy",
		TermsOfUseURL:            "https://contoso.example/terms",
		AgenticUserTemplate: &project.AgenticUserTemplateConfig{
			ID:                    "dw-template",
			File:                  "agenticUserTemplateManifest.json",
			SchemaVersion:         "0.1.0-preview",
			CommunicationProtocol: "activityProtocol",
		},
	}
	req := buildTeamsAppPackageRequest("/subscriptions/s/bot", teamsAppRequestOptions{
		scope:             scope,
		displayName:       "CLI Overridden",
		useCase:           project.ActivityUseCaseDigitalWorker,
		appVersion:        "",
		blueprintClientID: "blueprint-client-id",
		publish:           publish,
	})
	if !req.PublishAsAutopilot {
		t.Fatal("PublishAsAutopilot = false, want true")
	}
	if !req.UseAgenticUserTemplate {
		t.Fatal("UseAgenticUserTemplate = false, want true")
	}
	if req.AgenticUserTemplate == nil {
		t.Fatal("AgenticUserTemplate = nil, want template")
	}
	if req.AgenticUserTemplate.AgentIdentityBlueprintID != "blueprint-client-id" {
		t.Errorf(
			"AgentIdentityBlueprintID = %q, want blueprint-client-id",
			req.AgenticUserTemplate.AgentIdentityBlueprintID,
		)
	}
	if req.PublishScope != "Shared" {
		t.Errorf("PublishScope = %q, want Shared", req.PublishScope)
	}
	if req.AgentDisplayName != "CLI Overridden" {
		t.Errorf("AgentDisplayName = %q, want CLI Overridden", req.AgentDisplayName)
	}
	if req.AppVersion != "2.3.4" {
		t.Errorf("AppVersion = %q, want 2.3.4", req.AppVersion)
	}
	if req.ShortDescription != "A digital worker" {
		t.Errorf("ShortDescription = %q, want A digital worker", req.ShortDescription)
	}
}

func TestMicrosoft365APIVersion(t *testing.T) {
	simple := &teamsPackContext{
		activityProfile: project.ActivityProfile{IsActivity: true, UseCase: project.ActivityUseCaseSimple},
	}
	if got := microsoft365APIVersion(simple); got != agent_api.Microsoft365APIVersion {
		t.Errorf("simple API version = %q, want %q", got, agent_api.Microsoft365APIVersion)
	}

	digitalWorker := &teamsPackContext{
		activityProfile: project.ActivityProfile{
			IsActivity: true,
			UseCase:    project.ActivityUseCaseDigitalWorker,
		},
	}
	if got := microsoft365APIVersion(digitalWorker); got != agent_api.Microsoft365DigitalWorkerAPIVersion {
		t.Errorf(
			"digital worker API version = %q, want %q",
			got,
			agent_api.Microsoft365DigitalWorkerAPIVersion,
		)
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
