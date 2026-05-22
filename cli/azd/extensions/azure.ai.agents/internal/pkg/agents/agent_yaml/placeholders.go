// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"regexp"
	"slices"
)

// PlaceholderPattern captures {{NAME}} Mustache-style placeholders that
// injectParameterValues is supposed to replace before producing the
// final agent.yaml. Surviving placeholders are deploy-time landmines:
// the value lands in the container literally as `{{NAME}}`, breaking
// the agent.
//
// The capture group accepts any run of non-brace characters (allowing
// internal whitespace as long as the name starts with a non-whitespace,
// non-brace char) because injectParameterValues substitutes the raw
// manifest parameter key without validating its shape
// (`strings.ReplaceAll` of `{{<paramName>}}` and `{{ <paramName> }}`),
// and the YAML decoder assigns the raw key to Property.Name without
// validation either. A legitimate manifest parameter named
// `toolbox-endpoint` (hyphen), `my.param` (dot), or `"my key"` (quoted
// YAML key with whitespace) would otherwise slip past detection.
// Allows optional surrounding whitespace inside the braces — matches
// both `{{NAME}}` and `{{ NAME }}` (the two forms
// injectParameterValues knows how to substitute) plus more liberal
// spacing for forgiving detection.
//
// Shared between this package's post-substitution warning and the
// nextstep `Next:` guidance so the two stay in lockstep.
var PlaceholderPattern = regexp.MustCompile(`\{\{\s*([^\s{}][^{}]*?)\s*\}\}`)

// ExtractUnresolvedPlaceholders returns the deduplicated, sorted list
// of placeholder NAMES (i.e. the inside of `{{...}}`) that remain in
// template. An empty slice means the template is fully substituted.
//
// Used by both injectParameterValues (to surface a specific warning
// naming the unresolved placeholders) and by nextstep's fix-up
// generator (to surface the same names in the `Next:` block as a
// concrete "edit agent.yaml" hint). The two call sites must agree
// on what counts as a placeholder, hence the shared helper.
func ExtractUnresolvedPlaceholders(template string) []string {
	matches := PlaceholderPattern.FindAllStringSubmatch(template, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		name := m[1]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
