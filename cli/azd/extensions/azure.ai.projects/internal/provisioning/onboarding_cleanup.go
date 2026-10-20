// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)
func (p *FoundryProvisioningProvider) byoJournalValue(ctx context.Context) (string, error) {
	if value, present := p.virtualEnv[envKeyBYOJournal]; present && value != "" {
		return value, nil
	}
	if p.azdClient == nil {
		return "", nil
	}
	response, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: p.envName})
	if err != nil {
		return "", err
	}
	for _, value := range response.KeyValues {
		if value.Key == envKeyBYOJournal {
			return value.Value, nil
		}
	}
	return "", nil
}
