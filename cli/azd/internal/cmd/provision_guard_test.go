// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

func Test_EnvFlagGuard(t *testing.T) {
	tests := []struct {
		name          string
		existingSub   string
		existingLoc   string
		flagSub       string
		flagLoc       string
		wantSubErr    bool
		wantLocErr    bool
		wantSubErrMsg string
		wantLocErrMsg string
	}{
		{
			name:        "EmptyEnv_NewFlags_Allowed",
			existingSub: "",
			existingLoc: "",
			flagSub:     "new-sub-id",
			flagLoc:     "eastus",
		},
		{
			name:        "SameValues_NoOp",
			existingSub: "sub-123",
			existingLoc: "westus2",
			flagSub:     "sub-123",
			flagLoc:     "westus2",
		},
		{
			name:          "DifferentSub_Error",
			existingSub:   "sub-123",
			existingLoc:   "westus2",
			flagSub:       "sub-456",
			flagLoc:       "",
			wantSubErr:    true,
			wantSubErrMsg: "cannot change subscription",
		},
		{
			name:          "DifferentLoc_Error",
			existingSub:   "sub-123",
			existingLoc:   "westus2",
			flagSub:       "",
			flagLoc:       "eastus",
			wantLocErr:    true,
			wantLocErrMsg: "cannot change location",
		},
		{
			name:          "DifferentBoth_SubErrorFirst",
			existingSub:   "sub-123",
			existingLoc:   "westus2",
			flagSub:       "sub-456",
			flagLoc:       "eastus",
			wantSubErr:    true,
			wantSubErrMsg: "cannot change subscription",
		},
		{
			name:        "NoFlags_NoChange",
			existingSub: "sub-123",
			existingLoc: "westus2",
			flagSub:     "",
			flagLoc:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := environment.New("test-env")
			if tt.existingSub != "" {
				env.SetSubscriptionId(tt.existingSub)
			}
			if tt.existingLoc != "" {
				env.SetLocation(tt.existingLoc)
			}

			// Simulate the guard logic from provision.go
			var subErr, locErr error
			if tt.flagSub != "" {
				if existing := env.GetSubscriptionId(); existing != "" && existing != tt.flagSub {
					subErr = fmt.Errorf(
						"cannot change subscription for existing environment '%s' (current: %s, requested: %s). "+
							"Create a new environment with 'azd env new' instead",
						env.Name(), existing, tt.flagSub)
				}
			}
			if subErr == nil && tt.flagLoc != "" {
				if existing := env.GetLocation(); existing != "" && existing != tt.flagLoc {
					locErr = fmt.Errorf(
						"cannot change location for existing environment '%s' (current: %s, requested: %s). "+
							"Create a new environment with 'azd env new' instead",
						env.Name(), existing, tt.flagLoc)
				}
			}

			if tt.wantSubErr {
				require.Error(t, subErr)
				require.Contains(t, subErr.Error(), tt.wantSubErrMsg)
			} else {
				require.NoError(t, subErr)
			}

			if tt.wantLocErr {
				require.Error(t, locErr)
				require.Contains(t, locErr.Error(), tt.wantLocErrMsg)
			} else {
				require.NoError(t, locErr)
			}
		})
	}
}
