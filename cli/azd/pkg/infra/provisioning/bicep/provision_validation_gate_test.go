// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestResolveProvisionValidationGates verifies the config-gate split: the two
// independent keys `validation.provision` (local, client-side validation) and
// `provision.preflight` (server-side ARM preflight) each control only their own
// step, across all four on/off combinations.
func TestResolveProvisionValidationGates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                 string
		validationProvision  string // "" means key unset
		provisionPreflight   string // "" means key unset
		wantSkipValidation   bool
		wantSkipArmPreflight bool
	}{
		{
			name:                 "both unset → both run",
			wantSkipValidation:   false,
			wantSkipArmPreflight: false,
		},
		{
			name:                 "validation off only → local skipped, ARM preflight runs",
			validationProvision:  "off",
			wantSkipValidation:   true,
			wantSkipArmPreflight: false,
		},
		{
			name:                 "provision.preflight off only → ARM preflight skipped, local runs",
			provisionPreflight:   "off",
			wantSkipValidation:   false,
			wantSkipArmPreflight: true,
		},
		{
			name:                 "both off → both skipped",
			validationProvision:  "off",
			provisionPreflight:   "off",
			wantSkipValidation:   true,
			wantSkipArmPreflight: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.NewEmptyConfig()
			if tc.validationProvision != "" {
				require.NoError(t, cfg.Set("validation.provision", tc.validationProvision))
			}
			if tc.provisionPreflight != "" {
				require.NoError(t, cfg.Set("provision.preflight", tc.provisionPreflight))
			}

			skipValidation, skipArmPreflight := resolveProvisionValidationGates(cfg)
			require.Equal(t, tc.wantSkipValidation, skipValidation, "local validation gate")
			require.Equal(t, tc.wantSkipArmPreflight, skipArmPreflight, "ARM preflight gate")
		})
	}
}

// recordingDeployment records ValidatePreflight invocations. It embeds
// fakeDeployment (which panics on every unused method) and overrides only
// ValidatePreflight so the ARM-preflight gate can be observed.
type recordingDeployment struct {
	fakeDeployment
	preflightCalls atomic.Int32
}

func (r *recordingDeployment) ValidatePreflight(
	context.Context, azure.RawArmTemplate, azure.ArmParameters, map[string]*string, map[string]any,
) error {
	r.preflightCalls.Add(1)
	return nil
}

// TestValidateProvisionArmPreflightGate verifies that the server-side ARM
// preflight call (target.ValidatePreflight) is invoked only when its own gate
// permits it (skipArmPreflight=false), independent of the local-validation gate.
// The local-validation half is skipped here (skipLocalValidation=true) so the
// test isolates the ARM preflight gate without exercising the full client-side
// pipeline.
func TestValidateProvisionArmPreflightGate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		skipArmPreflight  bool
		wantPreflightCall int32
	}{
		{name: "ARM preflight runs when enabled", skipArmPreflight: false, wantPreflightCall: 1},
		{name: "ARM preflight skipped when disabled", skipArmPreflight: true, wantPreflightCall: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target := &recordingDeployment{}
			p := &BicepProvider{}

			canceled, err := p.validateProvision(
				context.Background(),
				target,
				"",
				azure.RawArmTemplate("{}"),
				azure.ArmParameters{},
				nil,
				nil,
				true, // skipLocalValidation — isolate the ARM preflight gate
				tc.skipArmPreflight,
			)

			require.NoError(t, err)
			require.False(t, canceled)
			require.Equal(t, tc.wantPreflightCall, target.preflightCalls.Load())
		})
	}
}
