package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// Use URL from https://learn.microsoft.com/en-us/azure/cloud-shell/msi-authorization
const cLocalTokenUrl = "http://localhost:50342/oauth2/token" //#nosec G101 -- This is a false positive

type TokenFromCloudShell struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	ExpiresIn    json.Number `json:"expires_in" type:"integer"`
	ExpiresOn    json.Number `json:"expires_on" type:"integer" `
	NotBefore    json.Number `json:"not_before" type:"integer" `
	Resource     string      `json:"resource"`
	TokenType    string      `json:"token_type"`
}

type CloudShellCredential struct {
}

func (t CloudShellCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	postData := url.Values{}
	postData.Set("resource", "https://management.azure.com/")

	req, err := http.NewRequestWithContext(
		ctx, "POST", cLocalTokenUrl, strings.NewReader(postData.Encode()))
	if err != nil {
		return azcore.AccessToken{}, err
	}
	req.Header.Add("Metadata", "true")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return azcore.AccessToken{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return azcore.AccessToken{}, fmt.Errorf("invalid CloudShell token API response code: %d", resp.StatusCode)
	}

	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return azcore.AccessToken{}, err
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
