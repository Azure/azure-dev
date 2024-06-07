package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// Use URL from https://learn.microsoft.com/azure/cloud-shell/msi-authorization
const cLocalTokenUrl = "http://localhost:50342/oauth2/token" //#nosec G101 -- This is a false positive

const cDefaultSuffix = "/.default"

type TokenFromCloudShell struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	ExpiresIn    json.Number `json:"expires_in"    type:"integer"`
	ExpiresOn    json.Number `json:"expires_on"    type:"integer"`
	NotBefore    json.Number `json:"not_before"    type:"integer"`
	Resource     string      `json:"resource"`
	TokenType    string      `json:"token_type"`
}

type CloudShellCredential struct {
	httpClient httputil.HttpClient
}

func NewCloudShellCredential(httpClient httputil.HttpClient) *CloudShellCredential {
	cloudShellCredential := CloudShellCredential{httpClient: httpClient}
	return &cloudShellCredential
}

func (t CloudShellCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	// Taken from azure_cli_credential.go
	if len(options.Scopes) != 1 {
		return azcore.AccessToken{}, errors.New("CloudShellCredential: GetToken() requires exactly one scope")
	}

	// API expects an AAD v1 resource, not a v2 scope
	scope := strings.TrimSuffix(options.Scopes[0], cDefaultSuffix)

	postData := url.Values{}
	postData.Set("resource", scope)

	req, err := http.NewRequestWithContext(
		ctx, "POST", cLocalTokenUrl, strings.NewReader(postData.Encode()))
	if err != nil {
		return azcore.AccessToken{}, err
	}
	req.Header.Add("Metadata", "true")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return azcore.AccessToken{}, err
	}
	defer resp.Body.Close()

	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return azcore.AccessToken{}, err
	}

	if resp.StatusCode != 200 {
		return azcore.AccessToken{}, fmt.Errorf(
			"invalid CloudShell token API response code: %d, content: %s",
			resp.StatusCode,
			responseBytes)
	}

	var tokenObject TokenFromCloudShell
	if err := json.Unmarshal(responseBytes, &tokenObject); err != nil {
		return azcore.AccessToken{}, err
	}

	expiresOnSeconds, err := tokenObject.ExpiresOn.Int64()
	if err != nil {
		return azcore.AccessToken{}, err
	}

	return azcore.AccessToken{
		Token:     tokenObject.AccessToken,
		ExpiresOn: time.Unix(expiresOnSeconds, 0),
	}, nil
}
