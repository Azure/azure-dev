// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"encoding/json"
	"fmt"

	"azure.ai.projects/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"
)

func deploymentParameters(parameters map[string]any) (map[string]*armdeployments.DeploymentParameter, error) {
	const suggestion = "Use ARM parameter entries with value, reference, or expression fields."
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return nil, exterrors.Validation(exterrors.CodeOnDiskParametersInvalid,
			fmt.Sprintf("encode ARM deployment parameters: %s", err), suggestion)
	}
	// Plain wire types reject unsupported fields that generated SDK decoders would silently discard.
	var wire map[string]*struct {
		Value      json.RawMessage `json:"value"`
		Expression *string         `json:"expression"`
		Reference  *struct {
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
		return nil, exterrors.Validation(exterrors.CodeOnDiskParametersInvalid,
			fmt.Sprintf("decode ARM deployment parameters: %s", err), suggestion)
	}
	if wire == nil {
		return nil, nil
	}
	result := make(map[string]*armdeployments.DeploymentParameter, len(wire))
	for name, entry := range wire {
		if entry == nil {
			return nil, exterrors.Validation(exterrors.CodeOnDiskParametersInvalid,
				fmt.Sprintf("ARM parameter %q must be an object", name), suggestion)
		}
		parameter := &armdeployments.DeploymentParameter{Expression: entry.Expression}
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
