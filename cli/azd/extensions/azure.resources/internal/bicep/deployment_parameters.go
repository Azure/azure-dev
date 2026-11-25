// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"
)

func deploymentParameters(parameters map[string]any) (map[string]*armdeployments.DeploymentParameter, error) {
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return nil, fmt.Errorf("encoding generated deployment parameters: %w", err)
	}
	// Preserve explicit nulls and numeric precision without silently dropping parameter fields.
	var wire map[string]*struct {
		Value     json.RawMessage `json:"value"`
		Reference *struct {
			KeyVault *struct {
				ID *string `json:"id"`
			} `json:"keyVault"`
			SecretName    *string `json:"secretName"`
			SecretVersion *string `json:"secretVersion"`
		} `json:"reference"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, fmt.Errorf("decoding generated deployment parameters: %w", err)
	}
	result := make(map[string]*armdeployments.DeploymentParameter, len(wire))
	for name, entry := range wire {
		if entry == nil {
			return nil, fmt.Errorf("generated deployment parameter %q must be an object", name)
		}
		parameter := &armdeployments.DeploymentParameter{}
		if len(entry.Value) > 0 {
			if bytes.Equal(entry.Value, []byte("null")) {
				parameter.Value = azcore.NullValue[*any]()
			} else {
				parameter.Value = entry.Value
			}
		}
		if reference := entry.Reference; reference != nil {
			parameter.Reference = &armdeployments.KeyVaultParameterReference{
				SecretName: reference.SecretName, SecretVersion: reference.SecretVersion,
			}
			if reference.KeyVault != nil {
				parameter.Reference.KeyVault = &armdeployments.KeyVaultReference{ID: reference.KeyVault.ID}
			}
		}
		result[name] = parameter
	}
	return result, nil
}
