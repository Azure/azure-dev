// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/stretchr/testify/assert"
)

func TestAuthStatusView_ToString(t *testing.T) {
	tests := []struct {
		name     string
		result   *contracts.StatusResult
		authMode string
		want     string
	}{
		{
			name: "unauthenticated with built-in mode",
			result: &contracts.StatusResult{
				Status: contracts.AuthStatusUnauthenticated,
			},
			authMode: "azd built in",
			want:     "Not logged in, run `azd auth login` to login to Azure",
		},
		{
			name: "unauthenticated with empty mode",
			result: &contracts.StatusResult{
				Status: contracts.AuthStatusUnauthenticated,
			},
			authMode: "",
			want:     "Not logged in, run `azd auth login` to login to Azure",
		},
		{
			name: "unauthenticated with az cli mode",
			result: &contracts.StatusResult{
				Status: contracts.AuthStatusUnauthenticated,
			},
			authMode: "az cli",
			want:     "Not logged in, run `az login` to login to Azure",
		},
		{
			name: "authenticated user",
			result: &contracts.StatusResult{
				Status: contracts.AuthStatusAuthenticated,
				Type:   contracts.AccountTypeUser,
				Email:  "user@example.com",
			},
			authMode: "azd built in",
		},
		{
			name: "authenticated service principal",
			result: &contracts.StatusResult{
				Status: contracts.AuthStatusAuthenticated,
				Type:   contracts.AccountTypeServicePrincipal,
				ClientID: "00000000-0000-0000-0000-000000000000",
			},
			authMode: "azd built in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &AuthStatusView{
				Result:   tt.result,
				AuthMode: tt.authMode,
			}
			got := v.ToString("")

			if tt.result.Status == contracts.AuthStatusUnauthenticated {
				assert.Equal(t, tt.want, got)
			} else {
				assert.Contains(t, got, "Logged in to Azure")
			}
		})
	}
}
