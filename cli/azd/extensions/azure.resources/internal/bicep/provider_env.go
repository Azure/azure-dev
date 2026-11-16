// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"

	"azure.resources/internal/environment"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
)

type scope struct {
	subscriptionID string
	tenantID       string
	location       string
	principalID    string
	principalType  string
}

// EnsureEnv resolves the active environment and prompts through the host for missing Azure scope.
func (p *Provider) EnsureEnv(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ensureEnv(ctx)
}

func (p *Provider) ensureEnv(ctx context.Context) error {
	if p.options == nil {
		return errors.New("azure.resources provider is not initialized")
	}
	if p.scope != nil {
		return nil
	}

	values, err := p.loadEnvironment(ctx)
	if err != nil {
		return err
	}
	azureCloud, err := p.loadCloud(ctx)
	if err != nil {
		return err
	}
	tenant, err := p.ensureSubscription(ctx, values)
	if err != nil {
		return err
	}
	if err := p.ensureLocation(ctx, values, tenant); err != nil {
		return err
	}
	resolved := &scope{
		subscriptionID: values["AZURE_SUBSCRIPTION_ID"], tenantID: tenant, location: values["AZURE_LOCATION"],
	}
	if err := p.resolvePrincipal(ctx, resolved); err != nil {
		return err
	}

	credential, err := azdext.NewTokenProvider(ctx, p.client, &azdext.TokenProviderOptions{TenantID: resolved.tenantID})
	if err != nil {
		return fmt.Errorf("creating Azure credential: %w", err)
	}
	if p.azure == nil {
		p.azure, err = newBackend(resolved.subscriptionID, credential,
			&arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: azureCloud.Configuration}})
		if err != nil {
			return err
		}
	}
	p.scope = resolved
	p.name = deploymentName(p.projectName, p.envName, p.options.Name)
	return nil
}

func (p *Provider) loadCloud(ctx context.Context) (*cloud.Cloud, error) {
	env, err := p.client.Environment().GetConfigString(ctx,
		&azdext.GetConfigStringRequest{EnvName: p.envName, Path: "cloud.name"})
	if err != nil {
		return nil, fmt.Errorf("reading environment cloud configuration: %w", err)
	}
	name := env.GetValue()
	if !env.GetFound() {
		project, err := p.client.Project().GetConfigValue(ctx, &azdext.GetProjectConfigValueRequest{Path: "cloud.name"})
		if err != nil {
			return nil, fmt.Errorf("reading project cloud configuration: %w", err)
		}
		if project.GetFound() {
			var ok bool
			name, ok = project.GetValue().AsInterface().(string)
			if !ok {
				return nil, errors.New("project cloud.name must be a string")
			}
		} else {
			user, err := p.client.UserConfig().GetString(ctx, &azdext.GetUserConfigStringRequest{Path: "cloud.name"})
			if err != nil {
				return nil, fmt.Errorf("reading user cloud configuration: %w", err)
			}
			name = user.GetValue()
		}
	}
	configured, err := cloud.NewCloud(&cloud.Config{Name: name})
	if err != nil {
		return nil, fmt.Errorf("resolving Azure cloud configuration: %w", err)
	}
	return configured, nil
}

func (p *Provider) loadEnvironment(ctx context.Context) (map[string]string, error) {
	name, err := environment.ResolveName(ctx, p.client.Environment(), "")
	if err != nil {
		return nil, fmt.Errorf("getting active environment: %w", err)
	}
	p.envName = name

	project, err := p.client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("getting project identity: %w", err)
	}
	p.projectName = project.GetProject().GetName()
	if p.projectName == "" {
		return nil, errors.New("a project name is required for deployment identity")
	}

	return p.loadEnvironmentValues(ctx)
}

func (p *Provider) loadEnvironmentValues(ctx context.Context) (map[string]string, error) {
	response, err := p.client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: p.envName})
	if err != nil {
		return nil, fmt.Errorf("reading environment values: %w", err)
	}
	values := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	for _, entry := range response.KeyValues {
		values[entry.Key] = entry.Value
	}
	maps.Copy(values, p.options.VirtualEnv)
	values["AZURE_ENV_NAME"] = p.envName
	return values, nil
}

func (p *Provider) ensureSubscription(ctx context.Context, values map[string]string) (string, error) {
	subscription := values["AZURE_SUBSCRIPTION_ID"]
	var tenant string
	if subscription == "" {
		selected, err := p.client.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
		if err != nil {
			return "", fmt.Errorf("selecting subscription: %w", err)
		}
		subscription = selected.GetSubscription().GetId()
		tenant = selected.GetSubscription().GetUserTenantId()
		if subscription == "" || tenant == "" {
			return "", errors.New("subscription selection returned no subscription or user access tenant")
		}
		values["AZURE_SUBSCRIPTION_ID"] = subscription
		if err := p.setEnv(ctx, "AZURE_SUBSCRIPTION_ID", subscription); err != nil {
			return "", err
		}
	}
	if tenant == "" {
		resolved, err := p.client.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: subscription})
		if err != nil {
			return "", fmt.Errorf("resolving subscription access tenant: %w", err)
		}
		tenant = resolved.GetTenantId()
	}
	if tenant == "" {
		return "", errors.New("subscription access tenant is empty")
	}
	return tenant, nil
}

func (p *Provider) ensureLocation(ctx context.Context, values map[string]string, tenant string) error {
	if values["AZURE_LOCATION"] != "" {
		return nil
	}

	selected, err := p.client.Prompt().PromptLocation(ctx, &azdext.PromptLocationRequest{
		AzureContext: azureContext(values["AZURE_SUBSCRIPTION_ID"], tenant),
	})
	if err != nil {
		return fmt.Errorf("selecting location: %w", err)
	}
	values["AZURE_LOCATION"] = selected.GetLocation().GetName()
	if values["AZURE_LOCATION"] == "" {
		return errors.New("location selection returned an empty location")
	}
	return p.setEnv(ctx, "AZURE_LOCATION", values["AZURE_LOCATION"])
}

func (p *Provider) resolvePrincipal(ctx context.Context, resolved *scope) error {
	principal, err := p.client.Account().GetCurrentPrincipal(ctx,
		&azdext.GetCurrentPrincipalRequest{SubscriptionId: resolved.subscriptionID})
	if err != nil {
		return fmt.Errorf("resolving current principal: %w", err)
	}
	resolved.principalID = principal.GetObjectId()
	switch principal.GetPrincipalType() {
	case azdext.PrincipalType_PRINCIPAL_TYPE_USER:
		resolved.principalType = "User"
	case azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL:
		resolved.principalType = "ServicePrincipal"
	default:
		return errors.New("current principal type is unknown")
	}
	if resolved.principalID == "" {
		return errors.New("current principal object ID is empty")
	}
	return nil
}

func azureContext(subscription, tenant string) *azdext.AzureContext {
	return &azdext.AzureContext{Scope: &azdext.AzureScope{SubscriptionId: subscription, TenantId: tenant}}
}

func (p *Provider) setEnv(ctx context.Context, key, value string) error {
	_, err := p.client.Environment().SetValue(ctx, &azdext.SetEnvRequest{EnvName: p.envName, Key: key, Value: value})
	if err != nil {
		return fmt.Errorf("saving environment value %s: %w", key, err)
	}
	return nil
}
