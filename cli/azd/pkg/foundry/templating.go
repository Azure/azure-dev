// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package foundry holds helpers shared across the Microsoft Foundry azd
// extensions (agents, projects, connections, toolboxes, skills, routines).
package foundry

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/drone/envsubst"
)

// foundryTemplatePattern matches Foundry server-side ${{...}} expressions. These are resolved
// by Foundry at runtime (for example ${{connections.x.credentials.key}} or ${{event.body}})
// and must survive azd's client-side ${VAR} expansion untouched. The (?s) flag lets the span
// cross newlines; the lazy quantifier stops at the first closing }}.
var foundryTemplatePattern = regexp.MustCompile(`(?s)\$\{\{.*?\}\}`)

// foundrySentinelBase is the prefix for placeholders that temporarily replace ${{...}} spans
// while ${VAR} expansion runs. It contains no '$', '{', or '}', so drone/envsubst (which only
// expands the braced ${...} form) copies it through untouched, even when a literal '$' precedes
// it.
const foundrySentinelBase = "azdFoundryTemplateSpan_"

// ExpandEnv expands ${VAR} references in value against the azd environment (via mapping) while
// preserving Foundry server-side ${{...}} expressions verbatim. It supports default values
// (${VAR:-default}) and multiple expressions, matching drone/envsubst semantics for the ${VAR}
// portion. On expansion error the original value is returned unchanged alongside the error.
//
// This is the single shared expander every Foundry field, in every Foundry extension, should
// route through so ${VAR} and ${{...}} are handled consistently. drone/envsubst cannot parse
// ${{...}}, so each span is masked with a sentinel placeholder, a single Eval expands the
// ${VAR} references, then the spans are restored. Masking rather than splitting preserves full
// ${VAR:-default} semantics even when a ${{...}} expression is the default value (e.g.
// ${MISSING:-${{event.body}}}). A ${VAR} inside a ${{...}} span is left as-is, since the span
// is reserved for Foundry.
func ExpandEnv(value string, mapping func(string) string) (string, error) {
	spans := foundryTemplatePattern.FindAllString(value, -1)
	if len(spans) == 0 {
		expanded, err := envsubst.Eval(value, mapping)
		if err != nil {
			return value, err
		}
		return expanded, nil
	}

	// Choose a sentinel that does not already occur in the input so restoration is exact.
	sentinel := foundrySentinelBase
	for strings.Contains(value, sentinel) {
		sentinel += "_"
	}

	index := 0
	masked := foundryTemplatePattern.ReplaceAllStringFunc(value, func(string) string {
		placeholder := fmt.Sprintf("%s%d_", sentinel, index)
		index++
		return placeholder
	})

	expanded, err := envsubst.Eval(masked, mapping)
	if err != nil {
		return value, err
	}

	for i, span := range spans {
		expanded = strings.Replace(expanded, fmt.Sprintf("%s%d_", sentinel, i), span, 1)
	}
	return expanded, nil
}
