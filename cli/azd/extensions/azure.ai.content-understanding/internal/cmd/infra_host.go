// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"azure.ai.content-understanding/internal/armclient"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/defaults"
	"azure.ai.content-understanding/internal/exterrors"
	"azure.ai.content-understanding/internal/infra"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type infraSnapshot struct {
	environment string
	values      map[string]string
	section     map[string]json.RawMessage
}

type infraHost interface {
	Subscription(context.Context, bool) (*azdext.Subscription, error)
	Resolve(context.Context, *azdext.Subscription, string) (armclient.Account, error)
	Snapshot(context.Context, string) (infraSnapshot, error)
	Capabilities(context.Context, infraSnapshot, infra.PostRequest) (infra.PostCapabilities, infra.ModelCapabilities, error)
	Close()
}

type infraHostFactory func(*cobra.Command, *azdext.ExtensionContext) (infraHost, error)

type azdInfraHost struct {
	client   *azdext.AzdClient
	noPrompt bool
}

// Generation must work without a CU endpoint or project. Only the host channel is opened here.
func openInfraHost(cmd *cobra.Command, extCtx *azdext.ExtensionContext) (infraHost, error) {
	if err := cmd.Context().Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(os.Getenv("AZD_SERVER")) == "" || strings.TrimSpace(os.Getenv("AZD_ACCESS_TOKEN")) == "" {
		return nil, exterrors.HostUnavailable()
	}
	client, err := azdext.NewAzdClient()
	if err != nil {
		return nil, exterrors.HostUnavailable()
	}
	return &azdInfraHost{client: client, noPrompt: extCtx.NoPrompt}, nil
}

func (h *azdInfraHost) Close() { h.client.Close() }

func (h *azdInfraHost) Subscription(ctx context.Context, noPrompt bool) (*azdext.Subscription, error) {
	if !noPrompt {
		response, err := h.client.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{
			Message: "Select the Azure subscription for the generated Content Understanding project",
		})
		if err != nil {
			return nil, err
		}
		subscription := response.GetSubscription()
		if subscription.GetId() == "" || subscription.GetTenantId() == "" {
			return nil, commandError(
				"invalid_subscription", "The host returned an incomplete subscription.", "Run azd auth login.")
		}
		return subscription, nil
	}
	response, err := h.client.Account().ListSubscriptions(ctx, &azdext.ListSubscriptionsRequest{})
	if err != nil {
		return nil, err
	}
	var selected *azdext.Subscription
	for _, subscription := range response.GetSubscriptions() {
		if subscription.GetIsDefault() {
			if selected != nil {
				return nil, commandError("ambiguous_subscription", "The host returned multiple default subscriptions.",
					"Set the default subscription with azd config set defaults.subscription <id>.")
			}
			selected = subscription
		}
	}
	if selected == nil && len(response.GetSubscriptions()) == 1 {
		selected = response.GetSubscriptions()[0]
	}
	if selected.GetId() == "" || selected.GetTenantId() == "" {
		return nil, commandError("subscription_required", "No unambiguous subscription is available without prompting.",
			"Set the azd default subscription or generate interactively.")
	}
	return selected, nil
}

func (h *azdInfraHost) Resolve(
	ctx context.Context, subscription *azdext.Subscription, resource string,
) (armclient.Account, error) {
	credential, err := azdext.NewTokenProvider(ctx, h.client,
		&azdext.TokenProviderOptions{TenantID: subscription.GetTenantId()})
	if err != nil {
		return armclient.Account{}, err
	}
	resolver, err := armclient.NewResolver(credential, nil)
	if err != nil {
		return armclient.Account{}, err
	}
	return resolver.Resolve(ctx, resource, subscription.GetId(), "", config.ResourceScope{})
}

func (h *azdInfraHost) Snapshot(ctx context.Context, name string) (infraSnapshot, error) {
	var selected *azdext.Environment
	if name == "" {
		response, err := h.client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return infraSnapshot{}, err
		}
		selected = response.GetEnvironment()
	} else {
		response, err := h.client.Environment().Get(ctx, &azdext.GetEnvironmentRequest{Name: name})
		if err != nil {
			return infraSnapshot{}, err
		}
		selected = response.GetEnvironment()
	}
	if selected.GetName() == "" || (name != "" && selected.GetName() != name) {
		return infraSnapshot{}, commandError("invalid_environment", "The host returned a different or empty environment.",
			"Select the generated project environment with --environment.")
	}
	snapshot := infraSnapshot{
		environment: selected.GetName(), values: map[string]string{}, section: map[string]json.RawMessage{},
	}
	response, err := h.client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: snapshot.environment})
	if err != nil {
		return infraSnapshot{}, err
	}
	if response == nil {
		return infraSnapshot{}, commandError(
			"invalid_environment", "The host returned no environment values.", "Run azd env refresh.")
	}
	for _, pair := range response.GetKeyValues() {
		if _, duplicate := snapshot.values[pair.GetKey()]; duplicate || pair.GetKey() == "" {
			return infraSnapshot{}, commandError("invalid_environment", "The host returned invalid or duplicate values.",
				"Run azd env refresh.")
		}
		snapshot.values[pair.GetKey()] = pair.GetValue()
	}
	section, err := h.client.Environment().GetConfigSection(ctx,
		&azdext.GetConfigSectionRequest{Path: "cu", EnvName: snapshot.environment})
	if err != nil {
		return infraSnapshot{}, err
	}
	if section == nil || (section.GetFound() &&
		(json.Unmarshal(section.GetSection(), &snapshot.section) != nil || snapshot.section == nil)) {
		return infraSnapshot{}, commandError("invalid_environment_config", "Existing cu configuration is not a JSON object.",
			"Repair the selected environment's cu configuration before retrying.")
	}
	return snapshot, nil
}

func (h *azdInfraHost) Capabilities(
	ctx context.Context, snapshot infraSnapshot, request infra.PostRequest,
) (infra.PostCapabilities, infra.ModelCapabilities, error) {
	tenant := snapshot.values["AZURE_TENANT_ID"]
	if tenant == "" {
		response, err := h.client.Account().LookupTenant(ctx,
			&azdext.LookupTenantRequest{SubscriptionId: request.Subscription})
		if err != nil {
			return nil, nil, err
		}
		tenant = response.GetTenantId()
	}
	if tenant == "" {
		return nil, nil, exterrors.MissingTenant()
	}
	credential, err := azdext.NewTokenProvider(ctx, h.client, &azdext.TokenProviderOptions{TenantID: tenant})
	if err != nil {
		return nil, nil, err
	}
	client, err := cuclient.NewSDKTransport(request.Endpoint, credential, &cuclient.TransportOptions{
		APIVersion: request.APIVersion, Version: Version,
	})
	if err != nil {
		return nil, nil, err
	}
	management, err := armclient.NewManagement(credential, config.ResourceScope{
		SubscriptionID: request.Subscription, ResourceGroup: request.ResourceGroup, ResourceName: request.Account,
	}, nil)
	if err != nil {
		return nil, nil, err
	}
	capabilities := &infraCapabilities{host: h, snapshot: snapshot, request: request, client: client, management: management}
	return capabilities, capabilities, nil
}

type modelManagement interface {
	Catalog(context.Context) ([]infra.CatalogModel, error)
	Deploy(context.Context, []infra.Model) error
	ListDeployments(context.Context) ([]infra.Deployment, error)
}

type infraCapabilities struct {
	host       *azdInfraHost
	snapshot   infraSnapshot
	request    infra.PostRequest
	client     cuclient.Client
	management modelManagement
}

func (c *infraCapabilities) Supported(ctx context.Context) (cuclient.Analyzer, error) {
	return c.client.GetAnalyzer(ctx, "prebuilt-document")
}
func (c *infraCapabilities) Catalog(ctx context.Context) ([]infra.CatalogModel, error) {
	return c.management.Catalog(ctx)
}
func (c *infraCapabilities) Deploy(ctx context.Context, models []infra.Model) error {
	return c.management.Deploy(ctx, models)
}
func (c *infraCapabilities) ListDeployments(ctx context.Context) ([]infra.Deployment, error) {
	return c.management.ListDeployments(ctx)
}
func (c *infraCapabilities) ConfigureDefaults(ctx context.Context, mappings map[string]string) error {
	_, err := defaults.Apply(ctx, c.client, mappings, false)
	return err
}
func (c *infraCapabilities) PromptModels(ctx context.Context, models []infra.Model) ([]infra.Model, error) {
	if c.host.noPrompt {
		return nil, commandError("model_selection_required", "Model selection requires an interactive decision.",
			"Set CU_MODEL_SELECTION to none, recommended, or explicit model@version selectors.")
	}
	selected := []infra.Model{}
	for _, model := range models {
		response, err := c.host.client.Prompt().Confirm(ctx, &azdext.ConfirmRequest{Options: &azdext.ConfirmOptions{
			Message: "Deploy " + model.Selector() + " (" + model.SKU + ")?", DefaultValue: new(false),
		}})
		if err != nil {
			return nil, err
		}
		if response == nil || response.Value == nil {
			return nil, commandError("invalid_confirmation_response", "The host returned no model deployment decision.",
				"Use an explicit --selection value.")
		}
		if response.GetValue() {
			for _, previous := range selected {
				if strings.EqualFold(previous.Name, model.Name) {
					return nil, commandError("duplicate_model_family", "Only one version of each family can be deployed.",
						"Retry with explicit model@version selectors.")
				}
			}
			selected = append(selected, model)
		}
	}
	return selected, ctx.Err()
}
func (c *infraCapabilities) SetupModels(ctx context.Context) error {
	if c.host.noPrompt && c.request.Selection == "prompt" {
		return commandError("model_selection_required", "Optional model setup requires a noninteractive selection.",
			"Set CU_MODEL_SELECTION to none, recommended, or explicit model@version selectors.")
	}
	_, err := infra.SetupModels(ctx, c, c.request.Selection, "infra/models.json", true, false)
	return err
}
func (c *infraCapabilities) MarkComplete(ctx context.Context) error {
	return c.setConfig(ctx, "cu.modelSetupComplete", true)
}
func (c *infraCapabilities) HasConfiguration(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, analyzer := c.snapshot.section["defaultAnalyzer"]
	_, models := c.snapshot.section["modelDeployments"]
	return analyzer || models, nil
}
func (c *infraCapabilities) setConfig(ctx context.Context, path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	response, err := c.host.client.Environment().SetConfig(ctx, &azdext.SetConfigRequest{
		EnvName: c.snapshot.environment, Path: path, Value: body,
	})
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("host returned no configuration write acknowledgment")
	}
	return nil
}
func (c *infraCapabilities) SetConfiguration(ctx context.Context, key, value string) error {
	if key == "endpoint" {
		response, err := c.host.client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: c.snapshot.environment, Key: "CU_ENDPOINT", Value: value,
		})
		if err != nil {
			return err
		}
		if response == nil {
			return errors.New("host returned no environment write acknowledgment")
		}
		return nil
	}
	if key == "default_analyzer" {
		return c.setConfig(ctx, "cu.defaultAnalyzer", value)
	}
	if model, ok := strings.CutPrefix(key, "model_deployments."); ok {
		mappings := map[string]string{}
		if raw, exists := c.snapshot.section["modelDeployments"]; exists {
			if err := json.Unmarshal(raw, &mappings); err != nil || mappings == nil {
				return errors.New("existing model mappings must be an object of strings")
			}
		}
		mappings[model] = value
		if err := c.setConfig(ctx, "cu.modelDeployments", mappings); err != nil {
			return err
		}
		raw, err := json.Marshal(mappings)
		if err != nil {
			return err
		}
		c.snapshot.section["modelDeployments"] = raw
		return nil
	}
	return fmt.Errorf("unsupported CU configuration key")
}
