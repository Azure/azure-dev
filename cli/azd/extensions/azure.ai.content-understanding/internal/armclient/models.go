// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package armclient

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/infra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

// Management uses the official Go management SDK (2025-06-01), not a custom LRO protocol.
type Management struct {
	accounts    *armcognitiveservices.AccountsClient
	deployments *armcognitiveservices.DeploymentsClient
	group       string
	account     string
}

type managementTransport struct {
	inner                       policy.Transporter
	origin, account, operations string
}

func (t managementTransport) Do(req *http.Request) (*http.Response, error) {
	u := req.URL
	path := strings.ToLower(u.Path)
	account := strings.ToLower(t.account)
	allowed := path == account+"/models" || path == account+"/deployments" ||
		strings.HasPrefix(path, account+"/deployments/") || strings.HasPrefix(path, strings.ToLower(t.operations))
	if u.Scheme != "https" || u.Host != t.origin || u.User != nil || u.Fragment != "" || !allowed ||
		strings.Contains(path, "/../") || strings.Contains(path, "/./") || strings.Contains(path, "\\") ||
		strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") ||
		(req.Method != http.MethodGet && req.Method != http.MethodPut) ||
		(req.Method == http.MethodPut && !strings.HasPrefix(path, account+"/deployments/")) {
		return nil, failure("invalid_arm_scope", "ARM management returned an unsafe operation URL.", nil)
	}
	return t.inner.Do(req)
}

type createOnlyPolicy struct{}

func (createOnlyPolicy) Do(req *policy.Request) (*http.Response, error) {
	if req.Raw().Method == http.MethodPut {
		req.Raw().Header.Set("If-None-Match", "*")
	}
	return req.Next()
}

func NewManagement(
	credential azcore.TokenCredential, scope config.ResourceScope, transport policy.Transporter,
) (*Management, error) {
	selector, err := ParseSelector(scope.ResourceName, scope.SubscriptionID, scope.ResourceGroup, config.ResourceScope{})
	if err != nil {
		return nil, err
	}
	if credential == nil || selector.Scope.SubscriptionID == "" || selector.Scope.ResourceGroup == "" {
		return nil, invalid("missing_management_scope",
			"Model management requires a credential and complete ARM account scope.")
	}
	scope = selector.Scope
	if transport == nil {
		transport = &http.Client{Timeout: 2 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	endpoint := cloud.AzurePublic.Services[cloud.ResourceManager].Endpoint
	origin, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	base := "/subscriptions/" + scope.SubscriptionID
	account := base + "/resourceGroups/" + scope.ResourceGroup + "/providers/Microsoft.CognitiveServices/accounts/" +
		scope.ResourceName
	options := &arm.ClientOptions{DisableRPRegistration: true, ClientOptions: policy.ClientOptions{
		Transport: boundedTransport{inner: managementTransport{
			inner: transport, origin: origin.Host, account: account,
			operations: base + "/providers/Microsoft.CognitiveServices/locations/",
		}},
		// Writes are not automatically replayed; SDK poller GETs also respect caller cancellation.
		Retry:           policy.RetryOptions{MaxRetries: -1, TryTimeout: 2 * time.Minute},
		PerCallPolicies: []policy.Policy{createOnlyPolicy{}},
	}}
	accounts, err := armcognitiveservices.NewAccountsClient(
		scope.SubscriptionID, discoveryCredential{inner: credential}, options)
	if err != nil {
		return nil, failure("arm_client_failed", "The ARM model client could not be initialized.", err)
	}
	deployments, err := armcognitiveservices.NewDeploymentsClient(
		scope.SubscriptionID, discoveryCredential{inner: credential}, options)
	if err != nil {
		return nil, failure("arm_client_failed", "The ARM deployment client could not be initialized.", err)
	}
	return &Management{
		accounts: accounts, deployments: deployments, group: scope.ResourceGroup, account: scope.ResourceName,
	}, nil
}

func (m *Management) Catalog(ctx context.Context) ([]infra.CatalogModel, error) {
	pager := m.accounts.NewListModelsPager(m.group, m.account, nil)
	result := []infra.CatalogModel{}
	for pages := 0; pager.More(); pages++ {
		if pages >= 1000 {
			return nil, failure("invalid_arm_paging", "ARM model catalog exceeded the paging limit.", nil)
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, failure("arm_catalog_failed", "Could not read the ARM model catalog.", err)
		}
		if page.Value == nil {
			return nil, failure("invalid_arm_catalog", "ARM returned no model catalog array.", nil)
		}
		for _, row := range page.Value {
			if row == nil {
				return nil, failure("invalid_arm_catalog", "ARM returned a null model entry.", nil)
			}
			model := infra.CatalogModel{Name: value(row.Name), Version: value(row.Version),
				Format: value(row.Format), Default: value(row.IsDefaultVersion)}
			for _, sku := range row.SKUs {
				if sku == nil {
					continue
				}
				capacity := int32(1)
				if sku.Capacity != nil {
					capacity = max(value(sku.Capacity.Default), 1)
				}
				model.SKUs = append(model.SKUs, infra.ModelSKU{Name: value(sku.Name), Capacity: capacity})
			}
			result = append(result, model)
		}
	}
	return result, nil
}

func (m *Management) ListDeployments(ctx context.Context) ([]infra.Deployment, error) {
	pager := m.deployments.NewListPager(m.group, m.account, nil)
	result := []infra.Deployment{}
	seen := map[string]bool{}
	for pages := 0; pager.More(); pages++ {
		if pages >= 1000 {
			return nil, failure("invalid_arm_paging", "ARM deployment listing exceeded the paging limit.", nil)
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, failure("arm_deployments_failed", "Could not inspect model deployments.", err)
		}
		if page.Value == nil {
			return nil, failure("invalid_arm_deployments", "ARM returned no deployment array.", nil)
		}
		for _, row := range page.Value {
			if row == nil || row.Properties == nil || row.Properties.Model == nil || value(row.Name) == "" {
				return nil, failure("invalid_arm_deployments", "ARM returned an incomplete deployment.", nil)
			}
			model := row.Properties.Model
			name := value(row.Name)
			if seen[strings.ToLower(name)] || value(model.Name) == "" {
				return nil, failure("invalid_arm_deployments", "ARM returned duplicate or invalid deployments.", nil)
			}
			seen[strings.ToLower(name)] = true
			result = append(result, infra.Deployment{Name: name, Model: value(model.Name),
				Version: value(model.Version), Format: value(model.Format),
				State: string(value(row.Properties.ProvisioningState))})
		}
	}
	return result, nil
}

// Deploy skips only succeeded exact matches and never overwrites or deletes an existing deployment.
func (m *Management) Deploy(ctx context.Context, models []infra.Model) error {
	if len(models) == 0 {
		return ctx.Err()
	}
	existing, err := m.ListDeployments(ctx)
	if err != nil {
		return err
	}
	pending := []infra.Model{}
	seen := map[string]bool{}
	for _, model := range models {
		if model.Name == "" || !scopeSegment.MatchString(model.Name) || model.Name == "." || model.Name == ".." ||
			model.Model == "" || model.Version == "" || model.Format == "" || model.SKU == "" || model.Capacity < 1 ||
			seen[strings.ToLower(model.Name)] {
			return invalid("invalid_deployment", "Model deployment input is invalid or duplicated.")
		}
		seen[strings.ToLower(model.Name)] = true
		found := false
		for _, deployment := range existing {
			if !strings.EqualFold(deployment.Name, model.Name) {
				continue
			}
			found = true
			if deployment.Model != model.Model || deployment.Version != model.Version ||
				(deployment.Format != "" && deployment.Format != model.Format) {
				return &cuclient.Error{Kind: cuclient.ErrorConflict, Code: "model_deployment_conflict",
					Message: "An existing deployment uses a different model or version; no deployment was overwritten."}
			}
			if !strings.EqualFold(deployment.State, "Succeeded") {
				return &cuclient.Error{Kind: cuclient.ErrorConflict, Code: "model_deployment_not_ready",
					Message: "An existing matching deployment is not succeeded; wait or repair it before retrying."}
			}
		}
		if !found {
			pending = append(pending, model)
		}
	}
	for _, model := range pending {
		poller, err := m.deployments.BeginCreateOrUpdate(ctx, m.group, m.account, model.Name,
			armcognitiveservices.Deployment{
				SKU: &armcognitiveservices.SKU{Name: new(model.SKU), Capacity: new(model.Capacity)},
				Properties: &armcognitiveservices.DeploymentProperties{Model: &armcognitiveservices.DeploymentModel{
					Name: new(model.Model), Version: new(model.Version), Format: new(model.Format),
				}},
			}, nil)
		if err != nil {
			return failure("model_deployment_failed",
				"Could not start a model deployment; inspect ARM before retrying.", err)
		}
		result, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Second})
		if err != nil {
			return failure("model_deployment_failed", "Model deployment did not finish; inspect ARM before retrying.", err)
		}
		if result.Properties == nil || !strings.EqualFold(string(value(result.Properties.ProvisioningState)), "Succeeded") {
			return failure("model_deployment_not_ready", "ARM did not confirm a succeeded model deployment.", nil)
		}
	}
	return ctx.Err()
}

func value[T any](pointer *T) (result T) {
	if pointer != nil {
		return *pointer
	}
	return result
}
