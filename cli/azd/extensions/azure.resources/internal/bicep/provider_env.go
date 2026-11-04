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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

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
	if p.values != nil {
		return nil
	}

	values, err := p.loadEnvironment(ctx)
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
	if err := p.resolvePrincipal(ctx, values); err != nil {
		return err
	}

	credential, err := azdext.NewTokenProvider(ctx, p.client, &azdext.TokenProviderOptions{TenantID: tenant})
	if err != nil {
		return fmt.Errorf("creating Azure credential: %w", err)
	}
	if p.azure == nil {
		p.azure, err = newBackend(values["AZURE_SUBSCRIPTION_ID"], credential)
		if err != nil {
			return err
		}
	}
	if p.secret == nil {
		p.secret = secretReader(credential)
	}

	p.values = values
	p.name = deploymentName(p.projectName, p.envName, p.options.Name)
	return nil
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

func (p *Provider) resolvePrincipal(ctx context.Context, values map[string]string) error {
	principal, err := p.client.Account().GetCurrentPrincipal(ctx,
		&azdext.GetCurrentPrincipalRequest{SubscriptionId: values["AZURE_SUBSCRIPTION_ID"]})
	if err != nil {
		return fmt.Errorf("resolving current principal: %w", err)
	}
	values["AZURE_PRINCIPAL_ID"] = principal.GetObjectId()
	switch principal.GetPrincipalType() {
	case azdext.PrincipalType_PRINCIPAL_TYPE_USER:
		values["AZURE_PRINCIPAL_TYPE"] = "User"
	case azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL:
		values["AZURE_PRINCIPAL_TYPE"] = "ServicePrincipal"
	default:
		return errors.New("current principal type is unknown")
	}
	if values["AZURE_PRINCIPAL_ID"] == "" {
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
