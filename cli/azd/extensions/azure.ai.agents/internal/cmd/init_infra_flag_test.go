// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInitInfraFlagParsing documents exactly how the --infra flag resolves for
// every argument form, given its NoOptDefVal="bicep" configuration. It parses
// against the real init command's flag set (no RunE execution) so the behavior
// is the cobra/pflag behavior users actually get.
func TestInitInfraFlagParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		args           []string
		wantInfra      string
		wantPositional []string
	}{
		{
			name:      "flag absent -> empty (no eject)",
			args:      []string{},
			wantInfra: "",
		},
		{
			name:      "bare --infra -> bicep (NoOptDefVal)",
			args:      []string{"--infra"},
			wantInfra: "bicep",
		},
		{
			name:      "--infra=terraform -> terraform",
			args:      []string{"--infra=terraform"},
			wantInfra: "terraform",
		},
		{
			name:      "--infra=bicep -> bicep (explicit)",
			args:      []string{"--infra=bicep"},
			wantInfra: "bicep",
		},
		{
			name:      "--infra=anything keeps the raw value (validated later)",
			args:      []string{"--infra=pulumi"},
			wantInfra: "pulumi",
		},
		{
			// IMPORTANT: with NoOptDefVal set, pflag does NOT consume a
			// space-separated value. `--infra terraform` resolves --infra to
			// its NoOptDefVal ("bicep") and treats "terraform" as a positional
			// argument. The equals form (--infra=terraform) is required.
			name:           "space-separated --infra terraform -> bicep + positional",
			args:           []string{"--infra", "terraform"},
			wantInfra:      "bicep",
			wantPositional: []string{"terraform"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newInitCommand(nil)
			require.NoError(t, cmd.Flags().Parse(tt.args))

			got, err := cmd.Flags().GetString("infra")
			require.NoError(t, err)
			assert.Equal(t, tt.wantInfra, got, "resolved --infra value")

			if tt.wantPositional != nil {
				assert.Equal(t, tt.wantPositional, cmd.Flags().Args(),
					"positional args after flag parsing")
			}
		})
	}
}
