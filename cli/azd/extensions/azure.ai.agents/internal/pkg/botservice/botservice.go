// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package botservice provisions and tears down the Azure Bot and Microsoft Teams
// channel that front an activity-protocol (Teams) agent. It ports the Azure
// resource-plane steps of setup-instance-bot.ps1 (create bot, enable Teams
// channel, set messaging endpoint) into the native azd deploy flow. Teams app
// packaging and install stay out of scope — they live on the M365/Graph plane.
package botservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice"
)

const (
	// botLocation is the ARM location for Azure Bot resources. Bots are global
	// and must be created at "global".
	botLocation = "global"
	// teamsChannelName is the discriminator value the Teams channel resource
	// carries in its ChannelName field.
	teamsChannelName = "MsTeamsChannel"
	// messagingEndpointAPIVersion is the api-version the activity-protocol
	// messaging endpoint URL is pinned to (verified against the POC).
	messagingEndpointAPIVersion = "2025-05-15-preview"
	// botNameSuffix is appended to the agent name to form the bot resource name.
	// BotService names are globally unique, so teardown must delete the bot.
	botNameSuffix = "-bot-uai"
)

// botsAPI and channelsAPI are the narrow slices of the armbotservice clients this
// package uses, extracted as interfaces so tests can substitute fakes.
type botsAPI interface {
	Create(
		ctx context.Context, resourceGroupName, resourceName string,
		parameters armbotservice.Bot, options *armbotservice.BotsClientCreateOptions,
	) (armbotservice.BotsClientCreateResponse, error)
	Delete(
		ctx context.Context, resourceGroupName, resourceName string,
		options *armbotservice.BotsClientDeleteOptions,
	) (armbotservice.BotsClientDeleteResponse, error)
}

type channelsAPI interface {
	Create(
		ctx context.Context, resourceGroupName, resourceName string,
		channelName armbotservice.ChannelName, parameters armbotservice.BotChannel,
		options *armbotservice.ChannelsClientCreateOptions,
	) (armbotservice.ChannelsClientCreateResponse, error)
}

// Client provisions and tears down the Azure Bot + Microsoft Teams channel for an
// activity-protocol agent. Its operations are idempotent and safe to run after
// every deploy.
type Client struct {
	bots     botsAPI
	channels channelsAPI
}

// NewClient builds a Client backed by the armbotservice SDK for a subscription.
func NewClient(
	subscriptionID string, cred azcore.TokenCredential, opts *arm.ClientOptions,
) (*Client, error) {
	bots, err := armbotservice.NewBotsClient(subscriptionID, cred, opts)
	if err != nil {
		return nil, fmt.Errorf("botservice: creating bots client: %w", err)
	}
	channels, err := armbotservice.NewChannelsClient(subscriptionID, cred, opts)
	if err != nil {
		return nil, fmt.Errorf("botservice: creating channels client: %w", err)
	}
	return &Client{bots: bots, channels: channels}, nil
}

// BotName returns the conventional Azure Bot resource name for an agent. It
// matches the name used by the sample's setup-instance-bot.ps1 so an existing
// bot is reused rather than duplicated.
func BotName(agentName string) string {
	return agentName + botNameSuffix
}

// MessagingEndpoint returns the activity-protocol messaging endpoint URL the bot
// forwards inbound Teams activities to.
func MessagingEndpoint(projectEndpoint, agentName string) string {
	return fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/activityProtocol?api-version=%s",
		strings.TrimRight(projectEndpoint, "/"), agentName, messagingEndpointAPIVersion,
	)
}

// BotConfig describes the Azure Bot to ensure for an activity-protocol agent.
type BotConfig struct {
	ResourceGroup string
	BotName       string
	// MsaAppID is the single-tenant app id the bot authenticates as. For the
	// simple use case this is the agent instance identity client id.
	MsaAppID          string
	TenantID          string
	MessagingEndpoint string
	// DisplayName is optional; BotName is used when empty.
	DisplayName string
}

func (cfg BotConfig) validate() error {
	var missing []string
	if cfg.ResourceGroup == "" {
		missing = append(missing, "ResourceGroup")
	}
	if cfg.BotName == "" {
		missing = append(missing, "BotName")
	}
	if cfg.MsaAppID == "" {
		missing = append(missing, "MsaAppID")
	}
	if cfg.TenantID == "" {
		missing = append(missing, "TenantID")
	}
	if cfg.MessagingEndpoint == "" {
		missing = append(missing, "MessagingEndpoint")
	}
	if len(missing) > 0 {
		return fmt.Errorf("botservice: missing required bot config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (cfg BotConfig) displayName() string {
	if cfg.DisplayName != "" {
		return cfg.DisplayName
	}
	return cfg.BotName
}

// EnsureBot idempotently creates (or updates) the single-tenant Azure Bot bound
// to MsaAppID and ensures the Microsoft Teams channel is enabled. The bot Create
// call is a PUT (create-or-update), so re-running after every deploy is a no-op
// when nothing changed and refreshes the messaging endpoint when it did.
func (c *Client) EnsureBot(ctx context.Context, cfg BotConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	bot := armbotservice.Bot{
		Location: new(botLocation),
		Kind:     to.Ptr(armbotservice.KindAzurebot),
		SKU:      &armbotservice.SKU{Name: to.Ptr(armbotservice.SKUNameF0)},
		Properties: &armbotservice.BotProperties{
			DisplayName:    new(cfg.displayName()),
			Endpoint:       new(cfg.MessagingEndpoint),
			MsaAppID:       new(cfg.MsaAppID),
			MsaAppType:     to.Ptr(armbotservice.MsaAppTypeSingleTenant),
			MsaAppTenantID: new(cfg.TenantID),
		},
	}

	if _, err := c.bots.Create(ctx, cfg.ResourceGroup, cfg.BotName, bot, nil); err != nil {
		return fmt.Errorf("botservice: creating/updating bot %q: %w", cfg.BotName, err)
	}

	return c.ensureTeamsChannel(ctx, cfg.ResourceGroup, cfg.BotName)
}

func (c *Client) ensureTeamsChannel(ctx context.Context, resourceGroup, botName string) error {
	channel := armbotservice.BotChannel{
		Location: new(botLocation),
		Properties: &armbotservice.MsTeamsChannel{
			ChannelName: new(teamsChannelName),
			Properties: &armbotservice.MsTeamsChannelProperties{
				IsEnabled: new(true),
			},
		},
	}

	if _, err := c.channels.Create(
		ctx, resourceGroup, botName, armbotservice.ChannelNameMsTeamsChannel, channel, nil,
	); err != nil {
		return fmt.Errorf("botservice: enabling Microsoft Teams channel on bot %q: %w", botName, err)
	}
	return nil
}

// DeleteBot removes the Azure Bot during teardown. A not-found response is
// treated as success so teardown is idempotent.
func (c *Client) DeleteBot(ctx context.Context, resourceGroup, botName string) error {
	if _, err := c.bots.Delete(ctx, resourceGroup, botName, nil); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("botservice: deleting bot %q: %w", botName, err)
	}
	return nil
}

func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}
