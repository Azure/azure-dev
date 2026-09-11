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
	"google.golang.org/grpc"
)

// connectionContext holds the resolved clients and project info for connection operations.
type connectionContext struct {
	armClient *armcognitiveservices.ProjectConnectionsClient
	dpClient  *connections.DataClient
	rg        string
	account   string
	project   string
	sub       string                 // subscription ID for raw REST calls
	cred      azcore.TokenCredential // credential for raw REST calls
	endpoint  string                 // Foundry project endpoint for readiness markers
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
	return resolveConnectionContextWithEnvironment(ctx, flagEndpoint, "")
}

// resolveConnectionContextWithEnvironment keeps an explicit selection consistent
// across endpoint lookup and ARM/tenant context. In particular, a destructive
// command must not fall back to another project's endpoint when that environment
// has no endpoint. An explicit endpoint flag still takes precedence; omitting the
// environment retains the standalone cascade and process-value fallback.
func resolveConnectionContextWithEnvironment(
	ctx context.Context,
	flagEndpoint, environmentName string,
) (*connectionContext, error) {
	if environmentName != "" && flagEndpoint == "" {
		return resolveConnectionContextForEnvironment(ctx, environmentName)
	}
	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{
		FlagValue: flagEndpoint, EnvironmentName: environmentName,
	})
	if err != nil {
		return nil, err
	}
	return newConnectionContext(ctx, resolved.Endpoint, environmentName)
}

// resolveConnectionContextForEnvironment is the lifecycle-only path. An absent
// endpoint must fail before creating clients or discovering ARM resources.
func resolveConnectionContextForEnvironment(
	ctx context.Context,
	environmentName string,
) (*connectionContext, error) {
	resolved, err := projectctx.ResolveEnvironment(ctx, environmentName)
	if err != nil {
		return nil, err
	}
	return newConnectionContext(ctx, resolved.Endpoint, environmentName)
}

func newConnectionContext(ctx context.Context, endpoint, environmentName string) (*connectionContext, error) {
	account, project, err := parseEndpointComponents(endpoint)
	if err != nil {
		return nil, err
	}

	// Resolve the azd environment context needed to build the clients:
	// the subscription's user-access tenant (for credential scoping) and the
	// Foundry project's ARM resource ID (for ARM context on connection-less
	// projects). Every field is best-effort and may be empty.
	envCtx := resolveEnvContext(ctx, environmentName)

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
		endpoint:  endpoint,
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
	// "" when the value is unavailable.
	projectID string
}

// resolveEnvContext best-effort reads the active or selected azd environment for the values
// needed to build the connection clients: the subscription's user-access tenant
// (credential scoping) and the Foundry project's ARM resource ID (ARM context
// for projects that have no connections yet).
//
// Lifecycle calls with an explicit environmentName read only persisted values.
// Standalone calls retain per-key process fallback after finding the active environment.
//
// Every field is optional. On a missing azd daemon, environment, or
// subscription the corresponding field is left empty and callers fall back to
// prior behavior. Scoping the credential to the subscription's tenant mirrors
// azure.ai.agents: LookupTenant returns the caller's access tenant for the
// subscription, which is the tenant that owns the Foundry resource - so
// multi-tenant / guest users get a token for that tenant instead of their home
// tenant.
func resolveEnvContext(ctx context.Context, environmentName string) envContext {
	var out envContext

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		log.Printf("connections: no azd client for environment resolution: %v", err)
		return out
	}
	defer azdClient.Close()

	return resolveEnvContextWithClients(
		ctx,
		environmentName,
		azdClient.Environment(),
		azdClient.Account(),
	)
}

type environmentContextReader interface {
	GetCurrent(
		context.Context,
		*azdext.EmptyRequest,
		...grpc.CallOption,
	) (*azdext.EnvironmentResponse, error)
	GetValue(
		context.Context,
		*azdext.GetEnvRequest,
		...grpc.CallOption,
	) (*azdext.KeyValueResponse, error)
	GetValues(
		context.Context,
		*azdext.GetEnvironmentRequest,
		...grpc.CallOption,
	) (*azdext.KeyValueListResponse, error)
}

type tenantLookup interface {
	LookupTenant(
		context.Context,
		*azdext.LookupTenantRequest,
		...grpc.CallOption,
	) (*azdext.LookupTenantResponse, error)
}

func resolveEnvContextWithClients(
	ctx context.Context,
	environmentName string,
	environmentClient environmentContextReader,
	accountClient tenantLookup,
) envContext {
	var out envContext
	var subID string
	envName := environmentName
	if envName == "" {
		envResp, err := environmentClient.GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil || envResp.GetEnvironment() == nil {
			log.Printf("connections: no active azd environment: %v", err)
			return out
		}
		envName = envResp.GetEnvironment().GetName()

		// Preserve standalone GetValue process fallback only after finding an active
		// environment. Each value is optional and a failed read must not block the other.
		getOptionalValue := func(key string) string {
			response, err := environmentClient.GetValue(ctx, &azdext.GetEnvRequest{EnvName: envName, Key: key})
			if err != nil {
				log.Printf("connections: unable to read %s from azd environment: %v", key, err)
				return ""
			}
			return response.GetValue()
		}
		out.projectID = getOptionalValue("AZURE_AI_PROJECT_ID")
		subID = getOptionalValue("AZURE_SUBSCRIPTION_ID")
	} else {
		// Read one persisted snapshot for lifecycle ARM context and credential scoping.
		// GetValue can fall back to another environment's process values even with EnvName set.
		response, err := environmentClient.GetValues(ctx, &azdext.GetEnvironmentRequest{Name: envName})
		if err != nil {
			log.Printf("connections: unable to read persisted azd environment context: %v", err)
			return out
		}
		for _, value := range response.GetKeyValues() {
			switch value.GetKey() {
			case "AZURE_AI_PROJECT_ID":
				out.projectID = value.GetValue()
			case "AZURE_SUBSCRIPTION_ID":
				subID = value.GetValue()
			}
		}
	}
	if subID == "" {
		log.Printf("connections: AZURE_SUBSCRIPTION_ID unavailable; using default tenant")
		return out
	}

	tenantResp, err := accountClient.LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subID,
	})
	if err != nil {
		log.Printf("connections: tenant lookup failed for subscription %s: %v", subID, err)
		return out
	}
	out.tenantID = tenantResp.GetTenantId()

	return out
}
