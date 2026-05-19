// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package connections is the toolboxes-extension copy of the Foundry projects
// data-plane client. It exposes a single connection lookup primitive — the
// only call site the toolbox commands need. See § 3.2 of the design spec for
// the duplication contract with azure.ai.agents.
package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"azure.ai.toolboxes/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

const projectsAPIVersion = "2025-11-15-preview"

// ConnectionType is the ARM category of a Foundry project connection.
type ConnectionType string

const (
	ConnectionTypeAzureOpenAI         ConnectionType = "AzureOpenAI"
	ConnectionTypeAzureBlob           ConnectionType = "AzureBlob"
	ConnectionTypeAzureStorageAccount ConnectionType = "AzureStorageAccount"
	ConnectionTypeCognitiveSearch     ConnectionType = "CognitiveSearch"
	ConnectionTypeContainerRegistry   ConnectionType = "ContainerRegistry"
	ConnectionTypeCosmosDB            ConnectionType = "CosmosDB"
	ConnectionTypeApiKey              ConnectionType = "ApiKey"
	ConnectionTypeAppConfig           ConnectionType = "AppConfig"
	ConnectionTypeAppInsights         ConnectionType = "AppInsights"
	ConnectionTypeCustomKeys          ConnectionType = "CustomKeys"
	ConnectionTypeRemoteTool          ConnectionType = "RemoteTool"
)

// CredentialType is the credential kind reported on a connection.
type CredentialType string

const (
	CredentialTypeApiKey               CredentialType = "ApiKey"
	CredentialTypeAAD                  CredentialType = "AAD"
	CredentialTypeCustomKeys           CredentialType = "CustomKeys"
	CredentialTypeSAS                  CredentialType = "SAS"
	CredentialTypeNone                 CredentialType = "None"
	CredentialTypeAgenticIdentityToken CredentialType = "AgenticIdentityToken"
)

// BaseCredentials is the credential subdocument on a Connection response.
// The toolbox surface does not consume credentials; it is included only for
// JSON-unmarshal fidelity.
type BaseCredentials struct {
	Type CredentialType `json:"type"`
	Key  string         `json:"key,omitempty"`
}

// Connection is the minimal projection of a Foundry project connection used
// by the toolbox commands.
type Connection struct {
	Name        string            `json:"name"`
	ID          string            `json:"id"`
	Type        ConnectionType    `json:"type"`
	Target      string            `json:"target"`
	IsDefault   bool              `json:"isDefault"`
	Credentials BaseCredentials   `json:"credentials"`
	Metadata    map[string]string `json:"metadata"`
}

// Client looks up project connections via the Foundry data plane.
type Client struct {
	endpoint string
	pipeline runtime.Pipeline
}

// New builds a connections client bound to the resolved project endpoint.
// The endpoint must already be normalized (https://<account>.services.ai.azure.com/api/projects/<project>).
func New(endpoint string, cred azcore.TokenCredential) (*Client, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint must not be empty")
	}

	userAgent := fmt.Sprintf("azd-ext-azure-ai-toolboxes/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader},
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-toolboxes",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &Client{endpoint: endpoint, pipeline: pipeline}, nil
}

// Get retrieves a single connection by name. Returns an azcore.ResponseError
// with StatusCode 404 when the connection is absent on the project.
func (c *Client) Get(ctx context.Context, name string) (*Connection, error) {
	target := fmt.Sprintf(
		"%s/connections/%s?api-version=%s",
		c.endpoint, url.PathEscape(name), projectsAPIVersion,
	)

	req, err := runtime.NewRequest(ctx, http.MethodGet, target)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var conn Connection
	if err := json.Unmarshal(body, &conn); err != nil {
		return nil, fmt.Errorf("failed to unmarshal connection response: %w", err)
	}
	return &conn, nil
}
