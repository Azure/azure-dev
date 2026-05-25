// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// rawConnectionProperties represents the JSON body for connection PUT requests
// that use auth types not covered by the ARM Go SDK (e.g., UserEntraToken,
// ProjectManagedIdentity, AgenticIdentityToken).
type rawConnectionProperties struct {
	AuthType string            `json:"authType"`
	Category string            `json:"category"`
	Target   string            `json:"target"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Audience string            `json:"audience,omitempty"`
}

type rawConnectionBody struct {
	Properties rawConnectionProperties `json:"properties"`
}

// rawCreateConnection performs a PUT to the ARM connections endpoint using raw REST,
// bypassing the typed ARM SDK. Used for auth types like UserEntraToken,
// ProjectManagedIdentity, and AgenticIdentityToken that lack SDK structs.
func rawCreateConnection(
	ctx context.Context,
	connCtx *connectionContext,
	name string,
	props rawConnectionProperties,
) error {
	apiVersion := "2025-04-01-preview"
	armURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/"+
			"providers/Microsoft.CognitiveServices/accounts/%s/projects/%s/"+
			"connections/%s?api-version=%s",
		connCtx.sub, connCtx.rg, connCtx.account, connCtx.project,
		url.PathEscape(name), apiVersion,
	)

	body := rawConnectionBody{Properties: props}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal connection body: %w", err)
	}

	pipeline := runtime.NewPipeline("azd-connection-raw", "1.0.0",
		runtime.PipelineOptions{
			PerCall: []policy.Policy{
				runtime.NewBearerTokenPolicy(connCtx.cred,
					[]string{"https://management.azure.com/.default"}, nil),
			},
		},
		&policy.ClientOptions{},
	)

	req, err := runtime.NewRequest(ctx, http.MethodPut, armURL)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Content-Type", "application/json")
	req.Raw().Body = io.NopCloser(bytes.NewReader(bodyBytes))
	req.Raw().ContentLength = int64(len(bodyBytes))

	resp, err := pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("ARM request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return runtime.NewResponseError(resp)
	}

	return nil
}

// parseKVMap parses "key=value" pairs into a map[string]string.
func parseKVMap(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	result := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		found := false
		for i := range len(pair) {
			if pair[i] == '=' {
				result[pair[:i]] = pair[i+1:]
				found = true
				break
			}
		}
		if !found {
			log.Printf("warning: ignoring malformed key=value pair: %q", pair)
		}
	}
	return result
}
