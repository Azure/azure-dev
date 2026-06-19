// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// M365 registration moves the work the legacy activity-protocol PowerShell
// scripts performed (post-provision OAuth2 grants + blueprint owner, post-deploy
// digital-worker publish + Teams backend configuration) into the native azd
// deploy lifecycle. These are Microsoft Graph / AzureML / Teams Developer Portal
// data-plane operations that register the activity agent's blueprint into M365 so
// the agent is reachable from channels (e.g. Teams).
//
// All steps are best-effort: they require elevated Graph / admin permissions that
// not every developer has, so a failure is surfaced as a warning and does not fail
// `azd provision` / `azd deploy`. This mirrors the existing agent-identity RBAC
// step, which also warns (rather than fails) on a 403.

const (
	graphBaseURL = "https://graph.microsoft.com/v1.0"
	graphScope   = "https://graph.microsoft.com/.default"
	aiAzureScope = "https://ai.azure.com/.default"
	teamsScope   = "https://dev.teams.microsoft.com/.default"

	// Well-known first-party app IDs that the blueprint service principal must be
	// granted delegated access to for inheritable tool scopes to work.
	apexAppID    = "5a807f24-c9de-44ee-a3a7-329e88a00ffc"
	prodMCPAppID = "ea9ffc3e-8a23-4a7d-836d-234d7c7565c1"

	// Delegated scopes granted to the blueprint SP on the prod MCP server SP.
	mcpGrantScopes = "McpServers.M365Admin.All McpServers.DASearch.All McpServers.WebSearch.All " +
		"McpServers.Files.All AgentTools.MOSEvents.All McpServers.Admin365Graph.All " +
		"McpServers.ERPAnalytics.All McpServers.DataverseCustom.All McpServers.Dataverse.All " +
		"McpServers.D365Service.All McpServers.D365Sales.All McpServers.Management.All " +
		"McpServersMetadata.Read.All McpServers.Developer.All McpServers.CopilotMCP.All " +
		"McpServers.OneDriveSharepoint.All McpServers.Mail.All McpServers.Teams.All " +
		"McpServers.Me.All McpServers.Calendar.All McpServers.SharepointLists.All " +
		"McpServers.Knowledge.All McpServers.Excel.All McpServers.Word.All " +
		"McpServers.PowerPoint.All"

	// Delegated scope granted to the blueprint SP on the APEX (AgentData) SP.
	apexGrantScope = "AgentData.ReadWrite"
)

// m365Client performs authenticated JSON requests against the Graph, AzureML and
// Teams Developer Portal data planes using credential-scoped bearer tokens.
type m365Client struct {
	cred azcore.TokenCredential
	http *http.Client
}

func newM365Client(cred azcore.TokenCredential) *m365Client {
	return &m365Client{
		cred: cred,
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// httpResult is the decoded outcome of an authenticated request.
type httpResult struct {
	statusCode int
	body       string
}

// ok reports whether the status code is a 2xx success.
func (r httpResult) ok() bool { return r.statusCode >= 200 && r.statusCode < 300 }

// do issues an authenticated request for the given token scope. A nil body sends
// no payload. The response body is always read so callers can string-match
// service error messages for idempotency.
func (c *m365Client) do(
	ctx context.Context,
	method, url, scope string,
	body any,
) (httpResult, error) {
	token, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		return httpResult{}, fmt.Errorf("failed to acquire token for %s: %w", scope, err)
	}

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return httpResult{}, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return httpResult{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return httpResult{}, fmt.Errorf("request to %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	return httpResult{statusCode: resp.StatusCode, body: string(raw)}, nil
}

// servicePrincipalObjectID resolves a service principal's object ID from its app
// (client) ID via Graph.
func (c *m365Client) servicePrincipalObjectID(ctx context.Context, appID string) (string, error) {
	url := fmt.Sprintf("%s/servicePrincipals(appId='%s')", graphBaseURL, appID)
	res, err := c.do(ctx, http.MethodGet, url, graphScope, nil)
	if err != nil {
		return "", err
	}
	if !res.ok() {
		return "", fmt.Errorf("graph returned %d resolving service principal for appId %s: %s",
			res.statusCode, appID, res.body)
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(res.body), &obj); err != nil {
		return "", fmt.Errorf("failed to parse service principal response: %w", err)
	}
	if obj.ID == "" {
		return "", fmt.Errorf("service principal object ID not found for appId %s", appID)
	}
	return obj.ID, nil
}

// applicationObjectID resolves an application's object ID from its app (client) ID.
func (c *m365Client) applicationObjectID(ctx context.Context, appID string) (string, error) {
	url := fmt.Sprintf("%s/applications(appId='%s')", graphBaseURL, appID)
	res, err := c.do(ctx, http.MethodGet, url, graphScope, nil)
	if err != nil {
		return "", err
	}
	if !res.ok() {
		return "", fmt.Errorf("graph returned %d resolving application for appId %s: %s",
			res.statusCode, appID, res.body)
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(res.body), &obj); err != nil {
		return "", fmt.Errorf("failed to parse application response: %w", err)
	}
	if obj.ID == "" {
		return "", fmt.Errorf("application object ID not found for appId %s", appID)
	}
	return obj.ID, nil
}

// createOAuth2Grant creates a delegated (AllPrincipals) OAuth2 permission grant
// from the blueprint SP (clientID) to a resource SP (resourceID). Existing grants
// are treated as success (idempotent).
func (c *m365Client) createOAuth2Grant(ctx context.Context, clientSPID, resourceSPID, scope string) error {
	body := map[string]any{
		"clientId":    clientSPID,
		"consentType": "AllPrincipals",
		"principalId": nil,
		"resourceId":  resourceSPID,
		"scope":       scope,
	}
	res, err := c.do(ctx, http.MethodPost, graphBaseURL+"/oauth2PermissionGrants", graphScope, body)
	if err != nil {
		return err
	}
	if res.ok() {
		return nil
	}
	if strings.Contains(res.body, "Permission entry already exists") {
		return nil
	}
	return fmt.Errorf("graph returned %d creating OAuth2 grant: %s", res.statusCode, res.body)
}

// EnsureBlueprintM365Provisioning performs the post-provision M365 setup for an
// activity-protocol agent's blueprint: delegated OAuth2 grants for inheritable tool
// scopes and adding the current user as an owner of the blueprint application.
//
// Best-effort: any failure is reported as a warning and nil is returned so that a
// missing Graph permission does not fail `azd provision`.
func EnsureBlueprintM365Provisioning(
	ctx context.Context,
	cred azcore.TokenCredential,
	blueprintAppID string,
) error {
	if strings.TrimSpace(blueprintAppID) == "" {
		return nil
	}

	fmt.Println()
	fmt.Println("Blueprint M365 registration (post-provision)")
	fmt.Printf("  Blueprint app ID: %s\n", blueprintAppID)

	client := newM365Client(cred)

	blueprintSPID, err := client.servicePrincipalObjectID(ctx, blueprintAppID)
	if err != nil {
		warnM365("resolve blueprint service principal", err)
		// Without the blueprint SP object ID neither grant can proceed; the owner
		// step is independent, so fall through to it.
	} else {
		ensureOAuth2Grants(ctx, client, blueprintSPID)
	}

	ensureBlueprintOwner(ctx, client, blueprintAppID)

	fmt.Println("✓ Blueprint M365 registration (post-provision) complete")
	return nil
}

// ensureOAuth2Grants grants the blueprint SP delegated access to the prod MCP and
// APEX service principals. Each grant is best-effort.
func ensureOAuth2Grants(ctx context.Context, client *m365Client, blueprintSPID string) {
	mcpSPID, err := client.servicePrincipalObjectID(ctx, prodMCPAppID)
	if err != nil {
		warnM365("resolve prod MCP service principal", err)
	} else if err := client.createOAuth2Grant(ctx, blueprintSPID, mcpSPID, mcpGrantScopes); err != nil {
		warnM365("create MCP OAuth2 grant", err)
	} else {
		fmt.Println("    ✓ MCP delegated scopes granted to blueprint SP")
	}

	apexSPID, err := client.servicePrincipalObjectID(ctx, apexAppID)
	if err != nil {
		warnM365("resolve APEX service principal", err)
	} else if err := client.createOAuth2Grant(ctx, blueprintSPID, apexSPID, apexGrantScope); err != nil {
		warnM365("create APEX OAuth2 grant", err)
	} else {
		fmt.Println("    ✓ APEX delegated scope granted to blueprint SP")
	}
}

// ensureBlueprintOwner adds the current signed-in user as an owner of the blueprint
// application. Best-effort; requires user-delegated auth (not a service principal).
func ensureBlueprintOwner(ctx context.Context, client *m365Client, blueprintAppID string) {
	meRes, err := client.do(ctx, http.MethodGet, graphBaseURL+"/me", graphScope, nil)
	if err != nil {
		warnM365("resolve current user", err)
		return
	}
	if !meRes.ok() {
		warnM365("resolve current user", fmt.Errorf("graph returned %d: %s", meRes.statusCode, meRes.body))
		return
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(meRes.body), &me); err != nil || me.ID == "" {
		warnM365("resolve current user", fmt.Errorf("could not parse /me response: %s", meRes.body))
		return
	}

	appObjectID, err := client.applicationObjectID(ctx, blueprintAppID)
	if err != nil {
		warnM365("resolve blueprint application", err)
		return
	}

	body := map[string]any{
		"@odata.id": fmt.Sprintf("%s/directoryObjects/%s", graphBaseURL, me.ID),
	}
	url := fmt.Sprintf("%s/applications/%s/owners/$ref", graphBaseURL, appObjectID)
	res, err := client.do(ctx, http.MethodPost, url, graphScope, body)
	if err != nil {
		warnM365("add current user as blueprint owner", err)
		return
	}
	if res.ok() || strings.Contains(res.body, "One or more added object references already exist") {
		fmt.Println("    ✓ Current user is an owner of the blueprint application")
		return
	}
	warnM365("add current user as blueprint owner",
		fmt.Errorf("graph returned %d: %s", res.statusCode, res.body))
}

// PublishDigitalWorkerParams holds the inputs required to publish an activity agent
// as an M365 digital worker and configure its Teams backend.
type PublishDigitalWorkerParams struct {
	BlueprintAppID string
	AgentGUID      string
	AgentName      string
	SubscriptionID string
	ResourceGroup  string
	Location       string
	AccountName    string
	ProjectName    string
}

// PublishDigitalWorker publishes the deployed activity agent as an M365 digital
// worker (tenant scope) and configures the blueprint's Teams backend so it is
// reachable from channels. Best-effort: failures are surfaced as warnings and nil
// is returned so a missing permission does not fail `azd deploy`.
func PublishDigitalWorker(
	ctx context.Context,
	cred azcore.TokenCredential,
	p PublishDigitalWorkerParams,
) error {
	if strings.TrimSpace(p.BlueprintAppID) == "" || strings.TrimSpace(p.AgentGUID) == "" {
		return nil
	}

	fmt.Println()
	fmt.Println("Publish digital worker (post-deploy)")
	fmt.Printf("  Agent: %s | Blueprint: %s | GUID: %s\n", p.AgentName, p.BlueprintAppID, p.AgentGUID)

	client := newM365Client(cred)

	if err := publishDigitalWorkerRequest(ctx, client, p); err != nil {
		warnM365("publish digital worker to M365", err)
	} else {
		fmt.Println("    ✓ Digital worker published to M365 (tenant scope)")
	}

	if err := configureBlueprintBackend(ctx, client, p.BlueprintAppID); err != nil {
		warnM365("configure blueprint backend in Teams Developer Portal", err)
	} else {
		fmt.Println("    ✓ Blueprint backend configured in Teams Developer Portal")
	}

	fmt.Println("✓ Publish digital worker (post-deploy) complete")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Approve the agent in M365 Admin Center: " +
		"https://admin.cloud.microsoft/#/agents/all/requested")
	fmt.Printf("  2. Blueprint ID: %s\n", p.BlueprintAppID)
	return nil
}

// publishDigitalWorkerRequest POSTs the M365 publish request to the AzureML
// agent-asset data plane. Existing published versions are treated as success.
func publishDigitalWorkerRequest(ctx context.Context, client *m365Client, p PublishDigitalWorkerParams) error {
	workspaceName := fmt.Sprintf("%s@%s@AML", p.AccountName, p.ProjectName)
	url := fmt.Sprintf(
		"https://%s.api.azureml.ms/agent-asset/v2.0/subscriptions/%s/resourceGroups/%s/"+
			"providers/Microsoft.MachineLearningServices/workspaces/%s/microsoft365/publish",
		p.Location, p.SubscriptionID, p.ResourceGroup, workspaceName,
	)

	body := map[string]any{
		"agentGuid":              p.AgentGUID,
		"botId":                  p.BlueprintAppID,
		"publishAsDigitalWorker": true,
		"appPublishScope":        "Tenant",
		"subscriptionId":         p.SubscriptionID,
		"agentName":              p.AgentName,
		"appVersion":             "1.0.0",
		"shortDescription":       "Foundry A365 Agent deployed via Azure Developer CLI",
		"fullDescription": "A Foundry A365 agent example that demonstrates integration with " +
			"Microsoft 365 and Azure Cognitive Services.",
		"developerName":          "Azure Developer",
		"developerWebsiteUrl":    "https://azure.microsoft.com",
		"privacyUrl":             "https://privacy.microsoft.com",
		"termsOfUseUrl":          "https://www.microsoft.com/legal/terms-of-use",
		"useAgenticUserTemplate": true,
		"agenticUserTemplate": map[string]any{
			"Id":                       "digitalWorkerTemplate",
			"File":                     "agenticUserTemplateManifest.json",
			"SchemaVersion":            "0.1.0-preview",
			"AgentIdentityBlueprintId": p.BlueprintAppID,
			"CommunicationProtocol":    "activityProtocol",
		},
	}

	res, err := client.do(ctx, http.MethodPost, url, aiAzureScope, body)
	if err != nil {
		return err
	}
	if res.ok() || strings.Contains(res.body, "version already exists") {
		return nil
	}
	return fmt.Errorf("publish API returned %d: %s", res.statusCode, res.body)
}

// configureBlueprintBackend configures the blueprint's bot-based backend in the
// Teams Developer Portal. The bot ID is the same as the blueprint app ID.
func configureBlueprintBackend(ctx context.Context, client *m365Client, blueprintAppID string) error {
	url := fmt.Sprintf(
		"https://dev.teams.microsoft.com/api/v1.0/agentblueprints/%s/backendConfiguration",
		blueprintAppID,
	)
	body := map[string]any{
		"type": "botBased",
		"botBased": map[string]any{
			"botId": blueprintAppID,
		},
	}
	res, err := client.do(ctx, http.MethodPut, url, teamsScope, body)
	if err != nil {
		return err
	}
	if res.ok() {
		return nil
	}
	return fmt.Errorf("teams developer portal returned %d: %s", res.statusCode, res.body)
}

// warnM365 prints a non-fatal warning for a failed best-effort M365 step.
func warnM365(action string, err error) {
	fmt.Printf("%s\n", output.WithWarningFormat(
		"    Could not %s: %v\n"+
			"      This step requires elevated Microsoft Graph / admin permissions and is best-effort.\n"+
			"      The agent was still deployed; complete this step manually if channel access is required.",
		action, err,
	))
}
