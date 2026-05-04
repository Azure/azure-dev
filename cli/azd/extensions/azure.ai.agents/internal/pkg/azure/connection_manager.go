// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armcognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

// ConnectionManager provides CRUD operations on connected resources in a Foundry project.
// It combines the ARM SDK client (for create/get/update/delete/list) with the data-plane
// client (for reading credentials/secrets). ARM never returns credential values; the
// data-plane getConnectionWithCredentials endpoint is the only way to retrieve them.
type ConnectionManager struct {
	armClient *armcognitiveservices.ProjectConnectionsClient
	dpClient  *FoundryProjectsClient // for reading credentials; nil if not needed
	rg        string
	account   string
	project   string
}

// NewConnectionManager creates a ConnectionManager with both ARM and data-plane clients.
// The dpClient is used for reading credentials via getConnectionWithCredentials.
func NewConnectionManager(
	subscriptionID, rg, account, project string,
	cred azcore.TokenCredential,
) (*ConnectionManager, error) {
	armClient, err := armcognitiveservices.NewProjectConnectionsClient(
		subscriptionID, cred, NewArmClientOptions(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating ARM connections client: %w", err)
	}

	dpClient, err := NewFoundryProjectsClient(account, project, cred)
	if err != nil {
		return nil, fmt.Errorf("creating data-plane client: %w", err)
	}

	return &ConnectionManager{
		armClient: armClient,
		dpClient:  dpClient,
		rg:        rg,
		account:   account,
		project:   project,
	}, nil
}

// ConnectionInfo holds metadata for a connection (no secrets).
type ConnectionInfo struct {
	Name      string
	ID        string
	Category  string
	AuthType  string
	Target    string
	IsDefault bool
	Metadata  map[string]string
}

// ConnectionDetail extends ConnectionInfo with credential key-value pairs
// retrieved from the data-plane getConnectionWithCredentials endpoint.
// The Credentials map contains only the actual secret fields (the "type"
// discriminator is excluded).
type ConnectionDetail struct {
	ConnectionInfo
	Credentials map[string]string
}

// CreateConnectionParams holds the parameters for creating a new connection.
type CreateConnectionParams struct {
	Category string            // connection category (e.g., "RemoteTool", "ApiKey", "CustomKeys")
	Target   string            // target URL or ARM resource ID
	AuthType string            // "ApiKey", "CustomKeys", or "None"
	Key      string            // API key value (used when AuthType is "ApiKey")
	Keys     map[string]string // custom key-value pairs (used when AuthType is "CustomKeys")
	Metadata map[string]string // optional metadata key-value pairs
}

// UpdateConnectionParams holds the parameters for updating an existing connection.
// Nil pointer fields mean "don't change". Map fields are merged with existing values.
type UpdateConnectionParams struct {
	Target   *string           // new target URL; nil = keep existing
	Key      *string           // new API key; nil = keep existing (ApiKey auth only)
	Keys     map[string]string // custom keys to add/overwrite (CustomKeys auth only)
	Metadata map[string]string // metadata to add/overwrite
}

// List returns all connections in the project, optionally filtered by category.
// Pass an empty string for category to list all connections.
func (m *ConnectionManager) List(ctx context.Context, category string) ([]ConnectionInfo, error) {
	opts := &armcognitiveservices.ProjectConnectionsClientListOptions{}
	if category != "" {
		opts.Category = &category
	}

	pager := m.armClient.NewListPager(m.rg, m.account, m.project, opts)

	var results []ConnectionInfo
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing connections: %w", err)
		}
		for _, r := range page.Value {
			results = append(results, connectionInfoFromARM(r))
		}
	}

	return results, nil
}

// Get returns metadata for a single connection (no credentials).
func (m *ConnectionManager) Get(ctx context.Context, name string) (*ConnectionInfo, error) {
	resp, err := m.armClient.Get(ctx, m.rg, m.account, m.project, name, nil)
	if err != nil {
		return nil, fmt.Errorf("getting connection %q: %w", name, err)
	}
	info := connectionInfoFromARM(&resp.ConnectionPropertiesV2BasicResource)
	return &info, nil
}

// GetWithCredentials returns metadata and credentials for a single connection.
// It calls both the ARM GET (metadata) and the data-plane getConnectionWithCredentials
// (secrets), then merges the results.
func (m *ConnectionManager) GetWithCredentials(ctx context.Context, name string) (*ConnectionDetail, error) {
	info, err := m.Get(ctx, name)
	if err != nil {
		return nil, err
	}

	creds, err := m.fetchCredentials(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("fetching credentials for %q: %w", name, err)
	}

	return &ConnectionDetail{
		ConnectionInfo: *info,
		Credentials:    creds,
	}, nil
}

// Create creates a new connection in the project.
// Supported auth types: ApiKey, CustomKeys, None. Returns an error for unsupported types.
func (m *ConnectionManager) Create(
	ctx context.Context, name string, params CreateConnectionParams,
) (*ConnectionInfo, error) {
	body, err := buildCreateBody(params)
	if err != nil {
		return nil, err
	}

	resp, err := m.armClient.Create(ctx, m.rg, m.account, m.project, name,
		&armcognitiveservices.ProjectConnectionsClientCreateOptions{
			Connection: body,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("creating connection %q: %w", name, err)
	}

	info := connectionInfoFromARM(&resp.ConnectionPropertiesV2BasicResource)
	return &info, nil
}

// Update updates an existing connection. It performs a GET-then-PUT because the ARM API
// does not support PATCH for connections. The update params are merged into the existing
// connection properties before the PUT.
func (m *ConnectionManager) Update(
	ctx context.Context, name string, params UpdateConnectionParams,
) (*ConnectionInfo, error) {
	// GET the current connection
	current, err := m.armClient.Get(ctx, m.rg, m.account, m.project, name, nil)
	if err != nil {
		return nil, fmt.Errorf("getting connection %q for update: %w", name, err)
	}

	updated, err := mergeUpdate(&current.ConnectionPropertiesV2BasicResource, params)
	if err != nil {
		return nil, err
	}

	resp, err := m.armClient.Create(ctx, m.rg, m.account, m.project, name,
		&armcognitiveservices.ProjectConnectionsClientCreateOptions{
			Connection: updated,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("updating connection %q: %w", name, err)
	}

	info := connectionInfoFromARM(&resp.ConnectionPropertiesV2BasicResource)
	return &info, nil
}

// Delete removes a connection from the project.
func (m *ConnectionManager) Delete(ctx context.Context, name string) error {
	_, err := m.armClient.Delete(ctx, m.rg, m.account, m.project, name, nil)
	if err != nil {
		return fmt.Errorf("deleting connection %q: %w", name, err)
	}
	return nil
}

// fetchCredentials calls the data-plane getConnectionWithCredentials endpoint
// and returns the credential fields as a flat map (excluding the "type" discriminator).
func (m *ConnectionManager) fetchCredentials(ctx context.Context, name string) (map[string]string, error) {
	conn, err := m.dpClient.GetConnectionWithCredentials(ctx, name)
	if err != nil {
		return nil, err
	}

	// The existing Connection.Credentials struct only has Type and Key fields,
	// but the raw response may contain arbitrary custom keys. We need to re-fetch
	// the raw response to get all credential fields.
	return m.fetchRawCredentials(ctx, name, conn)
}

// fetchRawCredentials extracts all credential key-value pairs from the data-plane
// response. For ApiKey auth, this is just {"key": "..."}.  For CustomKeys, this
// includes all named keys (e.g., {"x-api-key": "...", "secret": "..."}).
// The "type" discriminator is excluded from the result.
func (m *ConnectionManager) fetchRawCredentials(
	ctx context.Context, name string, conn *Connection,
) (map[string]string, error) {
	// For ApiKey connections, the existing struct captures the key
	if conn.Credentials.Type == CredentialTypeApiKey && conn.Credentials.Key != "" {
		return map[string]string{"key": conn.Credentials.Key}, nil
	}

	// For CustomKeys and other types, we need the raw JSON to get all fields.
	// Re-fetch using the raw response parser.
	raw, err := m.dpClient.getConnectionWithCredentialsRaw(ctx, name)
	if err != nil {
		return nil, err
	}

	// Parse the raw credentials JSON into a flat string map
	var envelope struct {
		Credentials map[string]json.RawMessage `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parsing credentials JSON: %w", err)
	}

	result := make(map[string]string, len(envelope.Credentials))
	for k, v := range envelope.Credentials {
		if k == "type" {
			continue // exclude the discriminator
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			continue // skip non-string values
		}
		result[k] = s
	}

	return result, nil
}

// connectionInfoFromARM converts the ARM SDK response type to our domain type.
func connectionInfoFromARM(r *armcognitiveservices.ConnectionPropertiesV2BasicResource) ConnectionInfo {
	info := ConnectionInfo{}
	if r.ID != nil {
		info.ID = *r.ID
		info.Name = lastSegment(*r.ID)
	}

	if r.Properties == nil {
		return info
	}

	props := r.Properties.GetConnectionPropertiesV2()
	if props == nil {
		return info
	}

	if props.Category != nil {
		info.Category = string(*props.Category)
	}
	if props.AuthType != nil {
		info.AuthType = string(*props.AuthType)
	}
	if props.Target != nil {
		info.Target = *props.Target
	}
	if props.IsSharedToAll != nil {
		info.IsDefault = *props.IsSharedToAll
	}
	if props.Metadata != nil {
		info.Metadata = make(map[string]string, len(props.Metadata))
		for k, v := range props.Metadata {
			if v != nil {
				info.Metadata[k] = *v
			}
		}
	}

	return info
}

// buildCreateBody constructs the ARM request body for creating a connection.
func buildCreateBody(params CreateConnectionParams) (*armcognitiveservices.ConnectionPropertiesV2BasicResource, error) {
	category := armcognitiveservices.ConnectionCategory(params.Category)
	metadata := toStringPtrMap(params.Metadata)

	switch params.AuthType {
	case "ApiKey":
		authType := armcognitiveservices.ConnectionAuthTypeAPIKey
		return &armcognitiveservices.ConnectionPropertiesV2BasicResource{
			Properties: &armcognitiveservices.APIKeyAuthConnectionProperties{
				AuthType: &authType,
				Category: &category,
				Target:   to.Ptr(params.Target),
				Credentials: &armcognitiveservices.ConnectionAPIKey{
					Key: to.Ptr(params.Key),
				},
				Metadata: metadata,
			},
		}, nil

	case "CustomKeys":
		authType := armcognitiveservices.ConnectionAuthTypeCustomKeys
		return &armcognitiveservices.ConnectionPropertiesV2BasicResource{
			Properties: &armcognitiveservices.CustomKeysConnectionProperties{
				AuthType: &authType,
				Category: &category,
				Target:   to.Ptr(params.Target),
				Credentials: &armcognitiveservices.CustomKeys{
					Keys: toStringPtrMap(params.Keys),
				},
				Metadata: metadata,
			},
		}, nil

	case "None":
		authType := armcognitiveservices.ConnectionAuthTypeNone
		return &armcognitiveservices.ConnectionPropertiesV2BasicResource{
			Properties: &armcognitiveservices.NoneAuthTypeConnectionProperties{
				AuthType: &authType,
				Category: &category,
				Target:   to.Ptr(params.Target),
				Metadata: metadata,
			},
		}, nil

	default:
		return nil, fmt.Errorf(
			"unsupported auth type %q; supported types: ApiKey, CustomKeys, None", params.AuthType,
		)
	}
}

// mergeUpdate applies UpdateConnectionParams onto an existing ARM resource for a PUT.
func mergeUpdate(
	current *armcognitiveservices.ConnectionPropertiesV2BasicResource,
	params UpdateConnectionParams,
) (*armcognitiveservices.ConnectionPropertiesV2BasicResource, error) {
	if current.Properties == nil {
		return nil, fmt.Errorf("connection has no properties to update")
	}

	props := current.Properties.GetConnectionPropertiesV2()
	if props == nil {
		return nil, fmt.Errorf("connection has no base properties")
	}

	// Update target
	if params.Target != nil {
		props.Target = params.Target
	}

	// Merge metadata
	if len(params.Metadata) > 0 {
		if props.Metadata == nil {
			props.Metadata = make(map[string]*string)
		}
		for k, v := range params.Metadata {
			props.Metadata[k] = to.Ptr(v)
		}
	}

	// Update credentials based on auth type
	if props.AuthType != nil {
		switch *props.AuthType {
		case armcognitiveservices.ConnectionAuthTypeAPIKey:
			if params.Key != nil {
				if apiKeyProps, ok := current.Properties.(*armcognitiveservices.APIKeyAuthConnectionProperties); ok {
					if apiKeyProps.Credentials == nil {
						apiKeyProps.Credentials = &armcognitiveservices.ConnectionAPIKey{}
					}
					apiKeyProps.Credentials.Key = params.Key
				}
			}
		case armcognitiveservices.ConnectionAuthTypeCustomKeys:
			if len(params.Keys) > 0 {
				if customProps, ok := current.Properties.(*armcognitiveservices.CustomKeysConnectionProperties); ok {
					if customProps.Credentials == nil {
						customProps.Credentials = &armcognitiveservices.CustomKeys{
							Keys: make(map[string]*string),
						}
					}
					if customProps.Credentials.Keys == nil {
						customProps.Credentials.Keys = make(map[string]*string)
					}
					for k, v := range params.Keys {
						customProps.Credentials.Keys[k] = to.Ptr(v)
					}
				}
			}
		}
	}

	return current, nil
}

// toStringPtrMap converts map[string]string to map[string]*string for the ARM SDK.
func toStringPtrMap(m map[string]string) map[string]*string {
	if m == nil {
		return nil
	}
	result := make(map[string]*string, len(m))
	for k, v := range m {
		result[k] = to.Ptr(v)
	}
	return result
}

// lastSegment returns the last "/" separated segment of a path.
func lastSegment(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
