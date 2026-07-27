// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry/projectctx"
	"azure.ai.connections/internal/pkg/connections"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// dataClient is a type alias for the data-plane client (used in endpoint.go).
type dataClient = connections.DataClient

// connectionContext holds the resolved clients and project info for connection operations.
type connectionContext struct {
	armClient *armcognitiveservices.ProjectConnectionsClient
	dpClient  *connections.DataClient
	rg        string
	account   string
	project   string
	sub       string                 // subscription ID for raw REST calls
	cred      azcore.TokenCredential // credential for raw REST calls
}

// resolveConnectionContext resolves the project endpoint, discovers ARM context,
// and creates both clients needed for connection operations.
//
// Endpoint resolution is delegated to projectctx.Resolve (the 5-level cascade
// shared with sibling Foundry extensions). The connection-specific work
// (account/project split, ARM discovery, client construction) stays here.
func resolveConnectionContext(
	ctx context.Context,
	flagEndpoint string,
) (*connectionContext, error) {
	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{FlagValue: flagEndpoint})
	if err != nil {
		return nil, err
	}
	endpoint := resolved.Endpoint

	account, project, err := parseEndpointComponents(endpoint)
	if err != nil {
		return nil, err
	}

	// Read the azd environment once for the values needed to build the clients:
	// the subscription's user-access tenant (for credential scoping) and the
	// Foundry project's ARM resource ID (for ARM context on connection-less
	// projects). Every field is best-effort and may be empty.
	envCtx := resolveEnvContext(ctx)

	// Scope the credential to the subscription's user-access tenant so tokens are
	// issued for the tenant that owns the Foundry resource. Multi-tenant / guest
	// users have a home tenant that differs from the resource tenant; without this
	// the data-plane and ARM calls below fail with "Tenant provided in token does
	// not match resource token". An empty tenant falls back to the caller's
	// default tenant (e.g. flag-only use outside an azd project).
	cred, err := newCredential(envCtx.tenantID)
	if err != nil {
		return nil, err
	}

	// Data-plane client (for list, get-with-credentials, and ARM discovery)
	dpClient := connections.NewDataClient(endpoint, cred)

	// Resolve the ARM subscription + resource group. Preferring the azd
	// environment's project resource ID lets the first connection be created on a
	// project that has none yet (azd up / `azd ai connection create`); discovery
	// from an existing connection is the fallback.
	armCtx, err := resolveARMContext(ctx, envCtx.projectID, account, project, dpClient)
	if err != nil {
		return nil, err
	}

	// ARM SDK client for CRUD
	armClient, err := armcognitiveservices.NewProjectConnectionsClient(
		armCtx.SubscriptionID, cred, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ARM connections client: %w", err)
	}

	return &connectionContext{
		armClient: armClient,
		dpClient:  dpClient,
		rg:        armCtx.ResourceGroup,
		account:   account,
		project:   project,
		sub:       armCtx.SubscriptionID,
		cred:      cred,
	}, nil
}

// newCredential creates an Azure credential for API calls. When tenantID is
// non-empty the credential is scoped to that tenant (with all other tenants
// additionally allowed), so multi-tenant / guest users get a token for the
// tenant that owns the Foundry resource. An empty tenantID uses the caller's
// default (home) tenant.
func newCredential(tenantID string) (azcore.TokenCredential, error) {
	options := &azidentity.AzureDeveloperCLICredentialOptions{}
	if tenantID != "" {
		options.TenantID = tenantID
		options.AdditionallyAllowedTenants = []string{"*"}
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(options)
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("Failed to create Azure credential: %s", err),
			"Run 'azd auth login' to authenticate.",
		)
	}

	return cred, nil
}

// envContext holds the azd-environment-derived values used to build the
// connection clients. Every field is optional; a zero value means the
// corresponding source was unavailable and callers fall back to prior behavior.
type envContext struct {
	// tenantID is the user-access tenant for AZURE_SUBSCRIPTION_ID; "" when the
	// subscription or tenant lookup is unavailable.
	tenantID string
	// projectID is AZURE_AI_PROJECT_ID (the Foundry project's ARM resource ID);
	// "" when the azd environment does not have it.
	projectID string
}

// resolveEnvContext best-effort reads the active azd environment for the values
// needed to build the connection clients: the subscription's user-access tenant
// (credential scoping) and the Foundry project's ARM resource ID (ARM context
// for projects that have no connections yet).
//
// Every field is optional. On a missing azd daemon, environment, or
// subscription the corresponding field is left empty and callers fall back to
// prior behavior. Scoping the credential to the subscription's tenant mirrors
// azure.ai.agents: LookupTenant returns the caller's access tenant for the
// subscription, which is the tenant that owns the Foundry resource - so
// multi-tenant / guest users get a token for that tenant instead of their home
// tenant.
func resolveEnvContext(ctx context.Context) envContext {
	var out envContext

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		log.Printf("connections: no azd client for environment resolution: %v", err)
		return out
	}
	defer azdClient.Close()

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp.GetEnvironment() == nil {
		log.Printf("connections: no active azd environment: %v", err)
		return out
	}
	envName := envResp.GetEnvironment().GetName()

	out.projectID = envValue(ctx, azdClient, envName, "AZURE_AI_PROJECT_ID")

	subID := envValue(ctx, azdClient, envName, "AZURE_SUBSCRIPTION_ID")
	if subID == "" {
		log.Printf("connections: AZURE_SUBSCRIPTION_ID unavailable; using default tenant")
		return out
	}

	tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subID,
	})
	if err != nil {
		log.Printf("connections: tenant lookup failed for subscription %s: %v", subID, err)
		return out
	}
	out.tenantID = tenantResp.GetTenantId()

	return out
}

// envValue reads a single value from the named azd environment, returning ""
// when the key is unset or the read fails.
func envValue(ctx context.Context, azdClient *azdext.AzdClient, envName, key string) string {
	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     key,
	})
	if err != nil {
		return ""
	}
	return resp.GetValue()
}
