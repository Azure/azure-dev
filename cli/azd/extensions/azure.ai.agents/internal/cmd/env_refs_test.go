// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// TestFindEnvironmentReferencesEscapedOuterKeepsInnerLive pins a
// case the two former scanners disagreed on: an escape neutralizes
// only the '${' it precedes, so a reference inside the escaped
// default is still live. foundry.ExpandEnv turns "$${A:-${B}}"
// into "${A:-<B>}".
func TestFindEnvironmentReferencesEscapedOuterKeepsInnerLive(t *testing.T) {
	t.Parallel()

	got, err := findEnvironmentReferences("$${A:-${B}}")
	require.NoError(t, err)
	require.Equal(t, []environmentReference{{Name: "B", Start: 6, End: 10}}, got)
}

func TestFindEnvironmentReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    []environmentReference
		wantErr string
	}{
		{
			name:  "bare reference",
			value: "${PLAIN}",
			want:  []environmentReference{{Name: "PLAIN", Start: 0, End: 8}},
		},
		{
			name:  "reference with default",
			value: "${NAME:-fallback}",
			want: []environmentReference{
				{Name: "NAME", Start: 0, End: 17, HasDefault: true},
			},
		},
		{
			name:  "multiple references keep order",
			value: "prefix ${ONE} mid ${TWO:-x} suffix",
			want: []environmentReference{
				{Name: "ONE", Start: 7, End: 13},
				{Name: "TWO", Start: 18, End: 27, HasDefault: true},
			},
		},
		{
			// drone/envsubst collapses '$' pairs, so one leading '$'
			// escapes the reference.
			name:  "single escape is dropped",
			value: "$${VAR}",
			want:  nil,
		},
		{
			// Two leading '$' collapse to a literal '$' and the
			// reference still expands.
			name:  "double escape still expands",
			value: "$$${VAR}",
			want:  []environmentReference{{Name: "VAR", Start: 2, End: 8}},
		},
		{
			name:  "triple escape is dropped",
			value: "$$$${VAR}",
			want:  nil,
		},
		{
			name:  "foundry expression yields nothing",
			value: "${{connections.store.credentials.key}}",
			want:  nil,
		},
		{
			name:  "reference inside a foundry expression is reserved",
			value: "${{ tools.${INNER} }}",
			want:  nil,
		},
		{
			name:  "foundry expression as a default value",
			value: "${MISSING:-${{event.body}}}",
			want: []environmentReference{
				{Name: "MISSING", Start: 0, End: 27, HasDefault: true},
			},
		},
		{
			name:    "nested default is rejected before discovery",
			value:   "${OUTER:-${NESTED}} ${AFTER}",
			wantErr: "unsupported environment variable reference",
		},
		{
			// A '$' the expander ignores must stay ignored here,
			// or a literal like "costs $price} today" writes a
			// phantom rice: ${rice} into the service env block.
			name:  "bare dollar is not a reference",
			value: "$foo}",
			want:  nil,
		},
		{
			// The phantom would span the whole string and carry
			// HasDefault, hiding REAL from init prompting.
			name:  "bare dollar keeps a later reference live",
			value: "$ab:-${REAL}}",
			want:  []environmentReference{{Name: "REAL", Start: 5, End: 12}},
		},
		{
			name:    "invalid name is rejected",
			value:   "${1BAD}",
			wantErr: "unsupported environment variable reference",
		},
		{
			name:    "unterminated reference is rejected",
			value:   "${UNCLOSED",
			wantErr: "unsupported environment variable reference",
		},
		{
			name:  "escaped shell operator stays literal",
			value: "$${BAR:=x}",
		},
		{
			name:  "shell operator in a Foundry expression stays reserved",
			value: "${{ event.body ?? '${BAR:=x}' }}",
		},
		{
			name:  "plain text yields nothing",
			value: "no references here",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := findEnvironmentReferences(tt.value)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestFindEnvironmentReferencesPolicies pins the one intended
// difference between the two consumers: init only prompts for
// references the expander cannot resolve on its own, while the
// generated service env block records every name so the owning
// extension can re-apply defaults.
func TestFindEnvironmentReferencesPolicies(t *testing.T) {
	t.Parallel()

	const value = "${BARE} ${WITH_DEFAULT:-fallback}"

	t.Run("init prompting skips defaults", func(t *testing.T) {
		t.Parallel()

		var references []azureYamlEnvironmentReference
		err := collectAzureYamlEnvironmentReferences(
			value,
			false,
			&references,
			map[string]int{},
		)
		require.NoError(t, err)
		require.Equal(t, []azureYamlEnvironmentReference{{Name: "BARE"}}, references)
	})

	t.Run("service env block records defaults", func(t *testing.T) {
		t.Parallel()

		environment := map[string]string{}
		require.NoError(t, collectStringEnvironmentTemplates(value, environment))
		require.Equal(t, map[string]string{
			"BARE":         "${BARE}",
			"WITH_DEFAULT": "${WITH_DEFAULT}",
		}, environment)
	})
}

func TestEnvironmentReferenceCollectorsRejectBeforeCollecting(t *testing.T) {
	t.Parallel()

	for _, invalid := range []string{"${BAR:=x}", "${OUTER:-${NESTED}}"} {
		t.Run(invalid, func(t *testing.T) {
			t.Parallel()
			value := "${VALID} " + invalid
			environment := map[string]string{"EXISTING": "${EXISTING}"}
			require.Error(t, collectStringEnvironmentTemplates(value, environment))
			require.Equal(t, map[string]string{"EXISTING": "${EXISTING}"}, environment)

			references := []azureYamlEnvironmentReference{{Name: "EXISTING"}}
			indexByName := map[string]int{"EXISTING": 0}
			err := collectAzureYamlEnvironmentReferences(value, false, &references, indexByName)
			require.Error(t, err)
			require.Equal(t, []azureYamlEnvironmentReference{{Name: "EXISTING"}}, references)
			require.Equal(t, map[string]int{"EXISTING": 0}, indexByName)
		})
	}
}

func TestEnvironmentReferenceDiagnosticsDoNotExposeValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"${API_URL:=https://test-user:test-password@example.test/path?sig=test-signature#test-fragment}",
		"${TOKEN:=test-secret}",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			content := fmt.Appendf(nil, `services:
  sample:
    host: azure.ai.connection
    credentials:
      key: '%s'
`, value)
			_, initErr := findAzureYamlEnvironmentReferences(content, ".")
			cfg, err := structpb.NewStruct(map[string]any{
				"credentials": map[string]any{"key": value},
			})
			require.NoError(t, err)
			server := &recordingProjectServer{}
			client := newProjectRecorderClient(t, server)
			serviceErr := addResourceService(t.Context(), client, "sample", AiConnectionHost, cfg, nil)

			for _, err := range []error{initErr, serviceErr} {
				require.ErrorContains(t, err, `service "sample"`)
				require.ErrorContains(t, err, "credentials.key:")
				require.ErrorContains(t, err, "unsupported environment variable reference")
				for _, sensitive := range []string{
					value, "test-user", "test-password", "sig=", "test-signature", "test-fragment", "test-secret",
				} {
					require.NotContains(t, err.Error(), sensitive)
				}
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Contains(t, localErr.Suggestion, "${VAR:-default}")
			}
		})
	}
}

func TestCollectAzureYamlEnvironmentReferencesUpgradesSecret(t *testing.T) {
	t.Parallel()

	var references []azureYamlEnvironmentReference
	indexByName := map[string]int{}
	require.NoError(t, collectAzureYamlEnvironmentReferences(
		"${SHARED}",
		false,
		&references,
		indexByName,
	))
	require.NoError(t, collectAzureYamlEnvironmentReferences(
		"${SHARED}",
		true,
		&references,
		indexByName,
	))

	require.Equal(
		t,
		[]azureYamlEnvironmentReference{{Name: "SHARED", Secret: true}},
		references,
	)
}

// TestFindEnvironmentReferencesMatchesExpander guards against the
// scanner drifting from foundry.ExpandEnv, which is what actually
// resolves these values at deploy. Every name the scanner reports
// must be one the expander asks for, so a generated env block
// never declares a variable the expander leaves alone. The corpus
// covers both opening shapes: '$' followed by '{', and a bare '$'
// the expander ignores.
func TestFindEnvironmentReferencesMatchesExpander(t *testing.T) {
	t.Parallel()

	values := []string{
		"${PLAIN}",
		"${NAME:-fallback}",
		"prefix ${ONE} mid ${TWO:-x} suffix",
		"$${ESCAPED}",
		"$$${DOUBLE_ESCAPED}",
		"$$$${TRIPLE_ESCAPED}",
		"$${OUTER:-${INNER}}",
		"${{connections.store.credentials.key}}",
		"${{ tools.${RESERVED} }}",
		"${MISSING:-${{event.body}}}",
		"${{f.g}}${AFTER}",
		"${BEFORE}${{f.g}}",
		"https://${HOST}/v1/${PATH:-default}",
		"$foo}",
		"prefix $bar} suffix",
		"costs $price} today",
		"$ab:-${REAL}}",
	}

	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			lookedUp := map[string]bool{}
			_, err := foundry.ExpandEnv(value, func(name string) string {
				lookedUp[name] = true
				return "value_" + name
			})
			require.NoError(t, err)

			references, err := findEnvironmentReferences(value)
			require.NoError(t, err)
			for _, reference := range references {
				require.Truef(
					t,
					lookedUp[reference.Name],
					"scanner reported %q but the expander never resolves it",
					reference.Name,
				)
			}
		})
	}
}
