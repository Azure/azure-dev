package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// RemoteCredential implements azcore.TokenCredential by using the remote credential protocol.
type RemoteCredential struct {
	// The endpoint of the remote endpoint to authenticate against.
	endpoint string
	// The key to use to authenticate against the remote endpoint.
	key string
	// Tenant ID to use to authenticate, instead of the default. Optional.
	tenantID string
	// The HTTP client to use to make requests.
	httpClient httputil.HttpClient
}

func newRemoteCredential(endpoint, key, tenantID string, httpClient httputil.HttpClient) *RemoteCredential {
	return &RemoteCredential{
		endpoint:   endpoint,
		key:        key,
		tenantID:   tenantID,
		httpClient: httpClient,
	}
}

func remoteCredentialError(err string, w error) error {
	return fmt.Errorf("RemoteCredential: %w", fmt.Errorf("%s: %w", err, w))
}

// GetToken implements azcore.TokenCredential.
func (rc *RemoteCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	tenantID := rc.tenantID
	if options.TenantID != "" {
		tenantID = options.TenantID
	}

	body, _ := json.Marshal(struct {
		Scopes   []string `json:"scopes"`
		TenantId string   `json:"tenantId,omitempty"`
	}{
		Scopes:   options.Scopes,
		TenantId: tenantID,
	})

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/token?api-version=2023-07-12-preview", rc.endpoint),
		bytes.NewReader(body))
	if err != nil {
		return azcore.AccessToken{}, remoteCredentialError("building request", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", rc.key))

	res, err := rc.httpClient.Do(req)
	if err != nil {
		return azcore.AccessToken{}, remoteCredentialError("making request", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return azcore.AccessToken{}, remoteCredentialError("unexpected status code", fmt.Errorf("%d", res.StatusCode))
	}

	var tokenResp struct {
		Status string `json:"status"`

		// These fields are set when status is "success"
		Token     *string    `json:"token,omitempty"`
		ExpiresOn *time.Time `json:"expiresOn,omitempty"`

		// These fields are set when the status "error"
		Code    *string `json:"code,omitempty"`
		Message *string `json:"message,omitempty"`
	}

	if err := json.NewDecoder(res.Body).Decode(&tokenResp); err != nil {
		return azcore.AccessToken{}, remoteCredentialError("decoding token response", err)
	}

	switch tokenResp.Status {
	case "success":
		return azcore.AccessToken{
			Token:     *tokenResp.Token,
			ExpiresOn: *tokenResp.ExpiresOn,
		}, nil
	case "error":
		return azcore.AccessToken{}, remoteCredentialError("failed to acquire token", fmt.Errorf("code: %s message: %s",
			*tokenResp.Code,
			*tokenResp.Message))
	default:
		return azcore.AccessToken{}, remoteCredentialError("unexpected status", fmt.Errorf(tokenResp.Status))
	}
}

var _ = azcore.TokenCredential(&RemoteCredential{})
