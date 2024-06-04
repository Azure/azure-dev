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
		return azcore.AccessToken{}, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", rc.key))

	res, err := rc.httpClient.Do(req)
	if err != nil {
		return azcore.AccessToken{}, fmt.Errorf("making request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return azcore.AccessToken{}, fmt.Errorf("unexpected status code: %d", res.StatusCode)
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
		return azcore.AccessToken{}, fmt.Errorf("unmarshalling response: %w", err)
	}

	switch tokenResp.Status {
	case "success":
		return azcore.AccessToken{
			Token:     *tokenResp.Token,
			ExpiresOn: *tokenResp.ExpiresOn,
		}, nil
	case "error":
		return azcore.AccessToken{}, fmt.Errorf("RemoteCredential: failed to acquire token: code: %s message: %s",
			*tokenResp.Code,
			*tokenResp.Message)
	default:
		return azcore.AccessToken{}, fmt.Errorf("RemoteCredential: unexpected status: %s", tokenResp.Status)
	}
}

var _ = azcore.TokenCredential(&RemoteCredential{})
