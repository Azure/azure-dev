// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	roleAzureAIUser           = "53ca6127-db72-4b80-b1b0-d745d6d5456d" // Foundry User
	roleCognitiveServicesUser = "a97b65f3-24c7-4388-baec-2e87135dc908" // Cognitive Services User
	roleAzureAIProjectManager = "eadc314b-1a2d-4efa-be10-5d325db5065e" // Foundry Project Manager
	roleFoundryOwner          = "c883944f-8b7b-4483-af10-35834be79c4a" // Foundry Owner

	roleAssignmentsAPIVersion = "2022-04-01"
	skillsPreviewHeaderValue  = "Skills=V1Preview"
)

var sufficientAIUserRoles = []string{
	roleAzureAIUser,
	roleCognitiveServicesUser,
	roleAzureAIProjectManager,
	roleFoundryOwner,
}

// PrepareDeveloperAccessOptions configures the developer RBAC pre-flight check.
type PrepareDeveloperAccessOptions struct {
	Endpoint        string
	EnvironmentName string
	Warn            func(string)
}

type environmentReader interface {
	GetCurrent(context.Context, *azdext.EmptyRequest, ...grpc.CallOption) (*azdext.EnvironmentResponse, error)
	GetValue(context.Context, *azdext.GetEnvRequest, ...grpc.CallOption) (*azdext.KeyValueResponse, error)
}

type accountReader interface {
	LookupTenant(context.Context, *azdext.LookupTenantRequest, ...grpc.CallOption) (*azdext.LookupTenantResponse, error)
	GetCurrentPrincipal(
		context.Context, *azdext.GetCurrentPrincipalRequest, ...grpc.CallOption,
	) (*azdext.GetCurrentPrincipalResponse, error)
}

type developerAccessConfig struct {
	credentialProvider func(tenantID string) (azcore.TokenCredential, error)
	clientOptions      *policy.ClientOptions
	propagationTimeout time.Duration
	initialDelay       time.Duration
}

// NewCredential returns the credential used by Foundry data-plane clients,
// optionally scoped to a specific tenant for guest/multi-tenant users.
func NewCredential(tenantID string) (azcore.TokenCredential, error) {
	options := &azidentity.AzureDeveloperCLICredentialOptions{}
	if tenantID != "" {
		options.TenantID = tenantID
		options.AdditionallyAllowedTenants = []string{"*"}
	}
	cred, err := azidentity.NewAzureDeveloperCLICredential(options)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	return cred, nil
}

// PrepareDeveloperAccess ensures the signed-in developer has Foundry User (or an
// equivalent data-plane role) on the environment-bound Foundry project before
// skill data-plane operations run, and returns a tenant-scoped credential when
// resolved. This mirrors azure.ai.agents' CheckDeveloperRBAC Check 1 so skill
// deployments and standalone commands do not fail with 403 before an agent
// predeploy hook runs.
//
// Missing roles and auto-assign failures are best-effort warnings so externally
// managed or custom-role environments can still proceed. Set
// AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true to skip this check.
func PrepareDeveloperAccess(
	ctx context.Context,
	options PrepareDeveloperAccessOptions,
) (azcore.TokenCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if skip, _ := strconv.ParseBool(os.Getenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")); skip {
		return nil, nil
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, ctx.Err()
	}
	defer azdClient.Close()

	return prepareDeveloperAccessWithClients(
		ctx,
		azdClient.Environment(),
		azdClient.Account(),
		options,
		developerAccessConfig{},
	)
}

func prepareDeveloperAccessWithClients(
	ctx context.Context,
	envClient environmentReader,
	accountClient accountReader,
	options PrepareDeveloperAccessOptions,
	cfg developerAccessConfig,
) (azcore.TokenCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if envClient == nil || accountClient == nil {
		return nil, nil
	}

	envName := strings.TrimSpace(options.EnvironmentName)
	if envName == "" {
		envResp, err := envClient.GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			if status.Code(err) == codes.NotFound || status.Code(err) == codes.Unavailable {
				return nil, nil
			}
			log.Printf("foundry: could not resolve current azd environment: %v", err)
			return nil, ctx.Err()
		}
		envName = strings.TrimSpace(envResp.GetEnvironment().GetName())
	}
	if envName == "" {
		return nil, nil
	}

	readEnv := func(key string) (string, error) {
		resp, err := envClient.GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envName,
			Key:     key,
		})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resp.GetValue()), nil
	}

	skipVal, err := readEnv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")
	if err != nil {
		log.Printf("foundry: could not read AZD_AGENT_SKIP_ROLE_ASSIGNMENTS: %v", err)
		return nil, ctx.Err()
	}
	if skip, parseErr := strconv.ParseBool(skipVal); parseErr == nil && skip {
		return nil, nil
	}

	projectID, err := readEnv("AZURE_AI_PROJECT_ID")
	if err != nil || projectID == "" {
		return nil, ctx.Err()
	}

	// Verify the target endpoint matches the environment's project before
	// inspecting or mutating role assignments on AZURE_AI_PROJECT_ID.
	envEndpoint, err := readEnv("FOUNDRY_PROJECT_ENDPOINT")
	if err != nil {
		return nil, ctx.Err()
	}
	if envEndpoint == "" {
		envEndpoint, err = readEnv("AZURE_AI_PROJECT_ENDPOINT")
		if err != nil {
			return nil, ctx.Err()
		}
	}
	if envEndpoint != "" && options.Endpoint != "" &&
		!strings.EqualFold(strings.TrimRight(envEndpoint, "/"), strings.TrimRight(options.Endpoint, "/")) {
		return nil, nil
	}

	parsedID, err := parseProjectResourceID(projectID, options.Endpoint)
	if err != nil {
		log.Printf("foundry: skipping developer RBAC check: %v", err)
		return nil, nil
	}

	tenantResp, err := accountClient.LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: parsedID.SubscriptionID,
	})
	if err != nil {
		log.Printf("foundry: could not resolve tenant for subscription %s: %v", parsedID.SubscriptionID, err)
		return nil, ctx.Err()
	}
	tenantID := strings.TrimSpace(tenantResp.GetTenantId())
	if tenantID == "" {
		return nil, nil
	}

	newCred := cfg.credentialProvider
	if newCred == nil {
		newCred = NewCredential
	}
	cred, err := newCred(tenantID)
	if err != nil {
		log.Printf("foundry: could not create credential: %v", err)
		return nil, nil
	}

	principalID, principalType, err := resolveDeveloperPrincipal(ctx, accountClient, parsedID.SubscriptionID)
	if err != nil {
		log.Printf("foundry: could not resolve current principal: %v", err)
		return cred, ctx.Err()
	}

	armPipeline := newARMPipeline(cred, cfg.clientOptions)
	hasAccess, err := hasAnyProjectRoleAssignment(
		ctx,
		armPipeline,
		parsedID.String(),
		principalID,
		sufficientAIUserRoles,
	)
	if err != nil {
		log.Printf("foundry: could not check Foundry User role: %v", err)
		return cred, ctx.Err()
	}
	if hasAccess {
		return cred, nil
	}

	created, assignErr := assignFoundryUserRole(
		ctx,
		armPipeline,
		parsedID.SubscriptionID,
		parsedID.String(),
		principalID,
		principalType,
	)
	if assignErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if options.Warn != nil {
			if respErr, ok := errors.AsType[*azcore.ResponseError](assignErr); ok &&
				respErr.StatusCode == http.StatusForbidden {
				options.Warn(fmt.Sprintf(
					"Your identity (%s) does not have the 'Foundry User' role on the Foundry Project %s/%s "+
						"and auto-assign was denied. Ask a subscription Owner or User Access Administrator "+
						"to run: az role assignment create --assignee %s --role %s --scope %q",
					principalID,
					parsedID.Parent.Name,
					parsedID.Name,
					principalID,
					roleAzureAIUser,
					parsedID.String(),
				))
			} else {
				options.Warn(fmt.Sprintf(
					"Foundry User auto-assign failed: %s — continuing",
					assignErr,
				))
			}
		}
		return cred, nil
	}

	if created && options.Endpoint != "" {
		if err := waitForSkillDataPlaneAccess(ctx, cred, options.Endpoint, principalID, cfg); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Printf("foundry: waiting for Foundry User RBAC propagation: %v", err)
		}
	}

	return cred, nil
}

func parseProjectResourceID(projectID, endpoint string) (*arm.ResourceID, error) {
	id, err := arm.ParseResourceID(projectID)
	if err != nil || id.Parent == nil || id.SubscriptionID == "" || id.ResourceGroupName == "" ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") {
		return nil, fmt.Errorf("invalid Foundry project resource ID")
	}
	if endpoint != "" {
		u, err := url.Parse(strings.TrimSpace(endpoint))
		if err != nil {
			return nil, err
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 3 && parts[0] == "api" && parts[1] == "projects" &&
			!strings.EqualFold(parts[2], id.Name) {
			return nil, fmt.Errorf("endpoint project %q does not match ARM project %q", parts[2], id.Name)
		}
	}
	return id, nil
}

func resolveDeveloperPrincipal(
	ctx context.Context,
	account accountReader,
	subscriptionID string,
) (string, string, error) {
	principal, err := account.GetCurrentPrincipal(ctx, &azdext.GetCurrentPrincipalRequest{
		SubscriptionId: subscriptionID,
	})
	if err != nil {
		return "", "", err
	}
	objectID := strings.TrimSpace(principal.GetObjectId())
	if objectID == "" {
		return "", "", fmt.Errorf("azd host returned an empty principal object ID")
	}
	switch principal.GetPrincipalType() {
	case azdext.PrincipalType_PRINCIPAL_TYPE_USER:
		return objectID, "User", nil
	case azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL:
		return objectID, "ServicePrincipal", nil
	default:
		return "", "", fmt.Errorf("unsupported principal type: %v", principal.GetPrincipalType())
	}
}

func newARMPipeline(cred azcore.TokenCredential, options *policy.ClientOptions) runtime.Pipeline {
	return runtime.NewPipeline(
		"azure-ai-skills-rbac",
		"v1.0.0",
		runtime.PipelineOptions{
			PerRetry: []policy.Policy{
				runtime.NewBearerTokenPolicy(cred, []string{"https://management.azure.com/.default"}, nil),
			},
		},
		options,
	)
}

func hasAnyProjectRoleAssignment(
	ctx context.Context,
	pipeline runtime.Pipeline,
	scope string,
	principalID string,
	roleIDs []string,
) (bool, error) {
	suffixes := make([]string, 0, len(roleIDs))
	for _, id := range roleIDs {
		suffixes = append(suffixes, "/roleDefinitions/"+id)
	}

	query := url.Values{
		"api-version": []string{roleAssignmentsAPIVersion},
		"$filter":     []string{fmt.Sprintf("assignedTo('%s')", principalID)},
	}
	nextURL := "https://management.azure.com" + scope +
		"/providers/Microsoft.Authorization/roleAssignments?" + query.Encode()

	for nextURL != "" {
		req, err := runtime.NewRequest(ctx, http.MethodGet, nextURL)
		if err != nil {
			return false, err
		}
		resp, err := pipeline.Do(req)
		if err != nil {
			return false, err
		}
		if resp.StatusCode != http.StatusOK {
			err := runtime.NewResponseError(resp)
			_ = resp.Body.Close()
			return false, err
		}
		var page struct {
			Value []struct {
				Properties struct {
					RoleDefinitionID string `json:"roleDefinitionId"`
				} `json:"properties"`
			} `json:"value"`
			NextLink string `json:"nextLink"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&page)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return false, decodeErr
		}
		for _, item := range page.Value {
			roleDef := item.Properties.RoleDefinitionID
			for _, suffix := range suffixes {
				if len(roleDef) >= len(suffix) &&
					strings.EqualFold(roleDef[len(roleDef)-len(suffix):], suffix) {
					return true, nil
				}
			}
		}
		nextURL = page.NextLink
	}

	return false, nil
}

func assignFoundryUserRole(
	ctx context.Context,
	pipeline runtime.Pipeline,
	subscriptionID string,
	scope string,
	principalID string,
	principalType string,
) (bool, error) {
	roleDefinitionID := fmt.Sprintf(
		"/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s",
		subscriptionID,
		roleAzureAIUser,
	)
	assignmentName := deterministicRoleAssignmentName(scope, principalID, roleDefinitionID)
	targetURL := fmt.Sprintf(
		"https://management.azure.com%s/providers/Microsoft.Authorization/roleAssignments/%s?api-version=%s",
		scope,
		url.PathEscape(assignmentName),
		roleAssignmentsAPIVersion,
	)

	payload, err := json.Marshal(map[string]any{
		"properties": map[string]string{
			"roleDefinitionId": roleDefinitionID,
			"principalId":      principalID,
			"principalType":    principalType,
		},
	})
	if err != nil {
		return false, err
	}

	req, err := runtime.NewRequest(ctx, http.MethodPut, targetURL)
	if err != nil {
		return false, err
	}
	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
		return false, err
	}

	resp, err := pipeline.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return true, nil
	}
	respErr := runtime.NewResponseError(resp)
	if resp.StatusCode == http.StatusConflict || strings.Contains(respErr.Error(), "RoleAssignmentExists") {
		return false, nil
	}
	return false, respErr
}

func deterministicRoleAssignmentName(scope, principalID, roleDefinitionID string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(scope + "\n" + principalID + "\n" + roleDefinitionID)))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return uuid.UUID(sum[:16]).String()
}

var deniedSkillReadPattern = regexp.MustCompile(
	`(?i)Identity\(object id:\s*([0-9a-f-]{36})\) does not have permissions for ` +
		`Microsoft\.CognitiveServices/accounts/AIServices/[a-z0-9_/]+/read actions\.`,
)

func waitForSkillDataPlaneAccess(
	ctx context.Context,
	cred azcore.TokenCredential,
	endpoint string,
	principalID string,
	cfg developerAccessConfig,
) error {
	timeout := cfg.propagationTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	delay := cfg.initialDelay
	if delay <= 0 {
		delay = time.Second
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-skills-rbac-probe",
		"v1.0.0",
		runtime.PipelineOptions{
			PerRetry: []policy.Policy{
				runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			},
		},
		cfg.clientOptions,
	)

	probeURL := strings.TrimRight(endpoint, "/") + "/skills?api-version=v1"
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		req, err := runtime.NewRequest(waitCtx, http.MethodGet, probeURL)
		if err != nil {
			return err
		}
		req.Raw().Header.Set("Foundry-Features", skillsPreviewHeaderValue)
		resp, err := pipeline.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusForbidden {
			_ = resp.Body.Close()
			return nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()

		matches := deniedSkillReadPattern.FindSubmatch(body)
		if len(matches) == 2 && !strings.EqualFold(string(matches[1]), principalID) {
			return nil
		}
		if len(matches) == 0 {
			// Not an AIServices RBAC read denial (e.g. network isolation 403); do not block.
			return nil
		}

		timer := time.NewTimer(delay)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return waitCtx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, 10*time.Second)
	}
}
