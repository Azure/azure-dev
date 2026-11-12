// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"regexp"
	"slices"
)

// PlaceholderPattern captures {{NAME}} Mustache-style placeholders.
// Surviving placeholders are deploy-time landmines because the value reaches
// the runtime literally instead of being resolved. The capture group accepts
// any run of non-brace characters and optional surrounding whitespace for
// forgiving detection.
var PlaceholderPattern = regexp.MustCompile(`\{\{\s*([^\s{}][^{}]*?)\s*\}\}`)

// ExtractUnresolvedPlaceholders returns the deduplicated, sorted list
// of placeholder NAMES (i.e. the inside of `{{...}}`) that remain in
// template. An empty slice means the template is fully substituted.
//
// nextstep uses this to surface a concrete "edit azure.yaml" hint.
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
