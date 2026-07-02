// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package botservice

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice"
)

type fakeBots struct {
	createCalls []armbotservice.Bot
	createErr   error
	deleteCalls int
	deleteErr   error
}

func (f *fakeBots) Create(
	_ context.Context, _, _ string,
	parameters armbotservice.Bot, _ *armbotservice.BotsClientCreateOptions,
) (armbotservice.BotsClientCreateResponse, error) {
	f.createCalls = append(f.createCalls, parameters)
	return armbotservice.BotsClientCreateResponse{}, f.createErr
}

func (f *fakeBots) Delete(
	_ context.Context, _, _ string, _ *armbotservice.BotsClientDeleteOptions,
) (armbotservice.BotsClientDeleteResponse, error) {
	f.deleteCalls++
	return armbotservice.BotsClientDeleteResponse{}, f.deleteErr
}

type fakeChannels struct {
	createCalls []armbotservice.ChannelName
	createErr   error
}

func (f *fakeChannels) Create(
	_ context.Context, _, _ string, channelName armbotservice.ChannelName,
	_ armbotservice.BotChannel, _ *armbotservice.ChannelsClientCreateOptions,
) (armbotservice.ChannelsClientCreateResponse, error) {
	f.createCalls = append(f.createCalls, channelName)
	return armbotservice.ChannelsClientCreateResponse{}, f.createErr
}

func validConfig() BotConfig {
	return BotConfig{
		ResourceGroup:     "rg1",
		BotName:           "echo-bot-uai",
		MsaAppID:          "client-id-123",
		TenantID:          "tenant-abc",
		MessagingEndpoint: "https://proj/agents/echo/endpoint/protocols/activityProtocol?api-version=2025-05-15-preview",
	}
}

func TestEnsureBotCreatesSingleTenantBotAndTeamsChannel(t *testing.T) {
	bots := &fakeBots{}
	channels := &fakeChannels{}
	c := &Client{bots: bots, channels: channels}

	if err := c.EnsureBot(context.Background(), validConfig()); err != nil {
		t.Fatalf("EnsureBot returned error: %v", err)
	}

	if len(bots.createCalls) != 1 {
		t.Fatalf("expected 1 bot create call, got %d", len(bots.createCalls))
	}
	got := bots.createCalls[0]
	if got.Properties == nil {
		t.Fatal("bot properties are nil")
	}
	if got.Properties.MsaAppType == nil || *got.Properties.MsaAppType != armbotservice.MsaAppTypeSingleTenant {
		t.Errorf("MsaAppType = %v, want SingleTenant", got.Properties.MsaAppType)
	}
	if got.Properties.MsaAppID == nil || *got.Properties.MsaAppID != "client-id-123" {
		t.Errorf("MsaAppID = %v, want client-id-123", got.Properties.MsaAppID)
	}
	if got.Properties.MsaAppTenantID == nil || *got.Properties.MsaAppTenantID != "tenant-abc" {
		t.Errorf("MsaAppTenantID = %v, want tenant-abc", got.Properties.MsaAppTenantID)
	}
	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != armbotservice.SKUNameF0 {
		t.Errorf("SKU = %v, want F0", got.SKU)
	}
	if got.Location == nil || *got.Location != "global" {
		t.Errorf("Location = %v, want global", got.Location)
	}

	if len(channels.createCalls) != 1 || channels.createCalls[0] != armbotservice.ChannelNameMsTeamsChannel {
		t.Errorf("expected MsTeamsChannel create, got %v", channels.createCalls)
	}
}

func TestEnsureBotIsIdempotentAcrossRuns(t *testing.T) {
	bots := &fakeBots{}
	channels := &fakeChannels{}
	c := &Client{bots: bots, channels: channels}

	for i := range 3 {
		if err := c.EnsureBot(context.Background(), validConfig()); err != nil {
			t.Fatalf("run %d: EnsureBot error: %v", i, err)
		}
	}
	// Create is a PUT (create-or-update); re-running is safe and re-applies
	// the same desired state each time.
	if len(bots.createCalls) != 3 || len(channels.createCalls) != 3 {
		t.Errorf("expected 3 bot + 3 channel calls, got %d + %d",
			len(bots.createCalls), len(channels.createCalls))
	}
}

func TestEnsureBotValidatesConfig(t *testing.T) {
	c := &Client{bots: &fakeBots{}, channels: &fakeChannels{}}
	cfg := validConfig()
	cfg.MsaAppID = ""
	err := c.EnsureBot(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected validation error for missing MsaAppID")
	}
}

func TestDeleteBotTreatsNotFoundAsSuccess(t *testing.T) {
	bots := &fakeBots{deleteErr: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
	c := &Client{bots: bots, channels: &fakeChannels{}}

	if err := c.DeleteBot(context.Background(), "rg1", "echo-bot-uai"); err != nil {
		t.Fatalf("expected nil error on 404, got %v", err)
	}
	if bots.deleteCalls != 1 {
		t.Errorf("expected 1 delete call, got %d", bots.deleteCalls)
	}
}

func TestDeleteBotPropagatesOtherErrors(t *testing.T) {
	bots := &fakeBots{deleteErr: errors.New("boom")}
	c := &Client{bots: bots, channels: &fakeChannels{}}

	if err := c.DeleteBot(context.Background(), "rg1", "echo-bot-uai"); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestBotName(t *testing.T) {
	if got := BotName("echo28ju3pm"); got != "echo28ju3pm-bot-uai" {
		t.Errorf("BotName = %q, want echo28ju3pm-bot-uai", got)
	}
}

func TestMessagingEndpoint(t *testing.T) {
	want := "https://proj.example.com/agents/echo/endpoint/protocols/activityProtocol?api-version=2025-05-15-preview"
	if got := MessagingEndpoint("https://proj.example.com/", "echo"); got != want {
		t.Errorf("MessagingEndpoint = %q, want %q", got, want)
	}
}
