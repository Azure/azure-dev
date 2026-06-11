// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"regexp"
	"strings"

	"github.com/drone/envsubst"
)

// foundryTemplatePattern matches Foundry server-side ${{...}} expressions. These are resolved
// by Foundry at runtime (for example ${{connections.x.credentials.key}} or ${{event.body}})
// and must survive azd's client-side ${VAR} expansion untouched. The (?s) flag lets the span
// cross newlines; the lazy quantifier stops at the first closing }}.
var foundryTemplatePattern = regexp.MustCompile(`(?s)\$\{\{.*?\}\}`)

// ExpandEnv expands ${VAR} references in value against the azd environment (via mapping) while
// preserving Foundry server-side ${{...}} expressions verbatim. It supports default values
// (${VAR:-default}) and multiple expressions, matching drone/envsubst semantics for the ${VAR}
// portion. On expansion error the original value is returned unchanged alongside the error.
//
// This is the single shared expander every Foundry field should route through so that ${VAR}
// and ${{...}} are handled consistently. drone/envsubst cannot parse ${{...}} and fails the
// whole string, so without this split any ${VAR} that appears alongside a ${{...}} span is
// silently left unexpanded. The string is split on ${{...}} spans, only the gaps between them
// are expanded, and the spans are reattached untouched.
func ExpandEnv(value string, mapping func(string) string) (string, error) {
	spans := foundryTemplatePattern.FindAllStringIndex(value, -1)
	if len(spans) == 0 {
		expanded, err := envsubst.Eval(value, mapping)
		if err != nil {
			return value, err
		}
		return expanded, nil
	}

	var sb strings.Builder
	cursor := 0
	for _, span := range spans {
		expanded, err := envsubst.Eval(value[cursor:span[0]], mapping)
		if err != nil {
			return value, err
		}
		sb.WriteString(expanded)
		sb.WriteString(value[span[0]:span[1]])
		cursor = span[1]
	}

	expanded, err := envsubst.Eval(value[cursor:], mapping)
	if err != nil {
		return value, err
	}
	sb.WriteString(expanded)

	return sb.String(), nil
}
