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

	got := findEnvironmentReferences("$${A:-${B}}", honorEnvironmentEscaping)
	require.Equal(t, []environmentReference{{Name: "B", Start: 6, End: 10}}, got)
}

func TestFindEnvironmentReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		value         string
		honorEscaping bool
		want          []environmentReference
	}{
		{
			name:          "bare reference",
			value:         "${PLAIN}",
			honorEscaping: honorEnvironmentEscaping,
			want:          []environmentReference{{Name: "PLAIN", Start: 0, End: 8}},
		},
		{
			name:          "reference with default",
			value:         "${NAME:-fallback}",
			honorEscaping: honorEnvironmentEscaping,
			want: []environmentReference{
				{Name: "NAME", Start: 0, End: 17, HasDefault: true},
			},
		},
		{
			name:          "multiple references keep order",
			value:         "prefix ${ONE} mid ${TWO:-x} suffix",
			honorEscaping: honorEnvironmentEscaping,
			want: []environmentReference{
				{Name: "ONE", Start: 7, End: 13},
				{Name: "TWO", Start: 18, End: 27, HasDefault: true},
			},
		},
		{
			// drone/envsubst collapses '$' pairs, so one leading '$'
			// escapes the reference.
			name:          "single escape is dropped",
			value:         "$${VAR}",
			honorEscaping: honorEnvironmentEscaping,
			want:          nil,
		},
		{
			// Two leading '$' collapse to a literal '$' and the
			// reference still expands.
			name:          "double escape still expands",
			value:         "$$${VAR}",
			honorEscaping: honorEnvironmentEscaping,
			want:          []environmentReference{{Name: "VAR", Start: 2, End: 8}},
		},
		{
			name:          "triple escape is dropped",
			value:         "$$$${VAR}",
			honorEscaping: honorEnvironmentEscaping,
			want:          nil,
		},
		{
			name:          "escapes ignored when the owner does not honor them",
			value:         "$${VAR}",
			honorEscaping: ignoreEnvironmentEscaping,
			want:          []environmentReference{{Name: "VAR", Start: 1, End: 7}},
		},
		{
			name:          "foundry expression yields nothing",
			value:         "${{connections.store.credentials.key}}",
			honorEscaping: honorEnvironmentEscaping,
			want:          nil,
		},
		{
			name:          "reference inside a foundry expression is reserved",
			value:         "${{ tools.${INNER} }}",
			honorEscaping: honorEnvironmentEscaping,
			want:          nil,
		},
		{
			// Protection rides on the same switch as escaping so the
			// scan matches whichever expander owns the field.
			name:          "foundry expression unprotected when escaping ignored",
			value:         "${{ tools.${INNER} }}",
			honorEscaping: ignoreEnvironmentEscaping,
			want:          []environmentReference{{Name: "INNER", Start: 10, End: 18}},
		},
		{
			name:          "foundry expression as a default value",
			value:         "${MISSING:-${{event.body}}}",
			honorEscaping: honorEnvironmentEscaping,
			want: []environmentReference{
				{Name: "MISSING", Start: 0, End: 27, HasDefault: true},
			},
		},
		{
			// The span covers the nested default so scanning resumes
			// after it. Collecting NESTED itself is a separate concern.
			name:          "nested default is spanned but not collected",
			value:         "${OUTER:-${NESTED}} ${AFTER}",
			honorEscaping: honorEnvironmentEscaping,
			want: []environmentReference{
				{Name: "OUTER", Start: 0, End: 19, HasDefault: true},
				{Name: "AFTER", Start: 20, End: 28},
			},
		},
		{
			name:          "invalid name yields nothing",
			value:         "${1BAD}",
			honorEscaping: honorEnvironmentEscaping,
			want:          nil,
		},
		{
			name:          "unterminated reference yields nothing",
			value:         "${UNCLOSED",
			honorEscaping: honorEnvironmentEscaping,
			want:          nil,
		},
		{
			name:          "plain text yields nothing",
			value:         "no references here",
			honorEscaping: honorEnvironmentEscaping,
			want:          nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := findEnvironmentReferences(tt.value, tt.honorEscaping)
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
			honorEnvironmentEscaping,
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

func TestCollectAzureYamlEnvironmentReferencesUpgradesSecret(t *testing.T) {
	t.Parallel()

	var references []azureYamlEnvironmentReference
	indexByName := map[string]int{}
	collectAzureYamlEnvironmentReferences(
		"${SHARED}",
		false,
		honorEnvironmentEscaping,
		&references,
		indexByName,
	)
	collectAzureYamlEnvironmentReferences(
		"${SHARED}",
		true,
		honorEnvironmentEscaping,
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
// never declares a variable the expander leaves alone.
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

			for _, reference := range findEnvironmentReferences(value, honorEnvironmentEscaping) {
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
