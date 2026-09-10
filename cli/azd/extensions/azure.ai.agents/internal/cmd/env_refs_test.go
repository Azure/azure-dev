// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/stretchr/testify/require"
)

// TestFindEnvironmentReferencesEscapedOuterKeepsInnerLive pins a
// case the two former scanners disagreed on: an escape neutralizes
// only the '${' it precedes, so a reference inside the escaped
// default is still live. foundry.ExpandEnv turns "$${A:-${B}}"
// into "${A:-<B>}".
func TestFindEnvironmentReferencesEscapedOuterKeepsInnerLive(t *testing.T) {
	t.Parallel()

	got := findEnvironmentReferences("$${A:-${B}}")
	require.Equal(t, []environmentReference{{Name: "B", Start: 6, End: 10}}, got)
}

func TestFindEnvironmentReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  []environmentReference
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
			// Nested references are unsupported by design. The span
			// covers the default so scanning resumes after it and
			// NESTED is never reported.
			name:  "nested default is spanned, inner name unsupported",
			value: "${OUTER:-${NESTED}} ${AFTER}",
			want: []environmentReference{
				{Name: "OUTER", Start: 0, End: 19, HasDefault: true},
				{Name: "AFTER", Start: 20, End: 28},
			},
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
			name:  "invalid name yields nothing",
			value: "${1BAD}",
			want:  nil,
		},
		{
			name:  "unterminated reference yields nothing",
			value: "${UNCLOSED",
			want:  nil,
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

			got := findEnvironmentReferences(tt.value)
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
		collectAzureYamlEnvironmentReferences(
			value,
			false,
			&references,
			map[string]int{},
		)
		require.Equal(t, []azureYamlEnvironmentReference{{Name: "BARE"}}, references)
	})

	t.Run("service env block records defaults", func(t *testing.T) {
		t.Parallel()

		environment := map[string]string{}
		collectStringEnvironmentTemplates(value, environment)
		require.Equal(t, map[string]string{
			"BARE":         "${BARE}",
			"WITH_DEFAULT": "${WITH_DEFAULT}",
		}, environment)
	})
}

// TestNestedDefaultIsNotDiscovered pins the agreed limitation:
// azd nested references are unsupported, so NESTED reaches
// neither consumer. Only the env block keeps the outer name;
// init prompting drops it too because it carries a default.
// Without this, ${OUTER:-${NESTED}} would look half-supported.
func TestNestedDefaultIsNotDiscovered(t *testing.T) {
	t.Parallel()

	const value = "${OUTER:-${NESTED}}"

	environment := map[string]string{}
	collectStringEnvironmentTemplates(value, environment)
	require.Equal(t, map[string]string{"OUTER": "${OUTER}"}, environment)

	var references []azureYamlEnvironmentReference
	collectAzureYamlEnvironmentReferences(
		value,
		false,
		&references,
		map[string]int{},
	)
	require.Empty(t, references)
}

func TestCollectAzureYamlEnvironmentReferencesUpgradesSecret(t *testing.T) {
	t.Parallel()

	var references []azureYamlEnvironmentReference
	indexByName := map[string]int{}
	collectAzureYamlEnvironmentReferences(
		"${SHARED}",
		false,
		&references,
		indexByName,
	)
	collectAzureYamlEnvironmentReferences(
		"${SHARED}",
		true,
		&references,
		indexByName,
	)

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

			for _, reference := range findEnvironmentReferences(value) {
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
