// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func newTestValidationExtension() *extensions.Extension {
	return &extensions.Extension{
		Id:        "test.validation",
		Namespace: "test",
		Capabilities: []extensions.CapabilityType{
			extensions.ValidationProviderCapability,
		},
	}
}

func TestValidationService_DispatchChecks_NoChecks(t *testing.T) {
	svc := &ValidationService{}

	results, ruleIDs, err := svc.DispatchChecks(
		t.Context(), "local-preflight", nil,
	)
	require.NoError(t, err)
	require.Nil(t, results)
	require.Nil(t, ruleIDs)
}

func TestValidationService_OnRegisterRequest_Validations(t *testing.T) {
	svc := &ValidationService{}
	ext := newTestValidationExtension()

	tests := []struct {
		name      string
		checkType string
		ruleID    string
		wantErr   bool
	}{
		{"empty_check_type", "", "rule1", true},
		{"empty_rule_id", "local-preflight", "", true},
		{"whitespace_check_type", "  ", "rule1", true},
		{"whitespace_rule_id", "local-preflight", "  ", true},
		{"valid", "local-preflight", "rule1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var registered []validationCheckEntry
			mu := &sync.Mutex{}
			resp, err := svc.onRegisterRequest(
				t.Context(),
				&azdext.RegisterValidationCheckRequest{
					CheckType: tt.checkType,
					RuleId:    tt.ruleID,
				},
				ext,
				nil, // broker not needed for registration-only test
				&registered,
				mu,
			)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
			}
		})
	}
}
