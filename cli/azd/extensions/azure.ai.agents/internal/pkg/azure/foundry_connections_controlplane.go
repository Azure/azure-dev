// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

// armConnectionsAPIVersion is the Microsoft.CognitiveServices control-plane
// api-version used to create project connections. The data-plane connections
// endpoint is read-only (list + getConnectionWithCredentials), so connection
// creation must go through ARM.
const armConnectionsAPIVersion = "2025-06-01"

// FoundryConnectionsARMClient creates project connections via the Azure
// Resource Manager (control plane). It hand-rolls the request rather than using
// the typed armcognitiveservices client because the generated auth-type structs
// force their own `authType` discriminator and cannot express newer values such
// as `ProjectManagedIdentity`.
type FoundryConnectionsARMClient struct {
	subscriptionID string
	pipeline       runtime.Pipeline
}

// NewFoundryConnectionsARMClient builds an ARM-backed connections client. The
// pipeline authenticates against the ARM audience for the credential's cloud.
func NewFoundryConnectionsARMClient(
	subscriptionID string,
	cred azcore.TokenCredential,
) (*FoundryConnectionsARMClient, error) {
	armClient, err := arm.NewClient("azure-ai-agents-connections", "v1.0.0", cred, NewArmClientOptions())
	if err != nil {
		return nil, fmt.Errorf("creating ARM client: %w", err)
	}
	return &FoundryConnectionsARMClient{
		subscriptionID: subscriptionID,
		pipeline:       armClient.Pipeline(),
	}, nil
}

// ProjectConnectionProperties is the minimal `properties` envelope for creating
// a project connection through ARM.
type ProjectConnectionProperties struct {
	Category string            `json:"category"`
	Target   string            `json:"target"`
	AuthType string            `json:"authType"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// UpsertProjectConnection creates (or updates) a connection under a Foundry
// project. It is idempotent: re-running with the same name updates the existing
// connection in place.
func (c *FoundryConnectionsARMClient) UpsertProjectConnection(
	ctx context.Context,
	resourceGroup, accountName, projectName, connectionName string,
	props ProjectConnectionProperties,
) error {
	target := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/"+
			"Microsoft.CognitiveServices/accounts/%s/projects/%s/connections/%s?api-version=%s",
		url.PathEscape(c.subscriptionID),
		url.PathEscape(resourceGroup),
		url.PathEscape(accountName),
		url.PathEscape(projectName),
		url.PathEscape(connectionName),
		armConnectionsAPIVersion,
	)

	payload, err := json.Marshal(map[string]any{"properties": props})
	if err != nil {
		return fmt.Errorf("failed to marshal connection request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPut, target)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(payload)),
		"application/json",
	); err != nil {
		return fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return runtime.NewResponseError(resp)
	}
	// Drain the body so the connection can be reused by the pipeline.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
