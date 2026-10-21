// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/drone/envsubst/parse"
)

var foundryTemplatePattern = regexp.MustCompile(`(?s)\$\{\{.*?\}\}`)

const foundrySentinelBase = "azdFoundryTemplateSpan_"

// EnvReferenceAnalysis describes environment template references.
type EnvReferenceAnalysis struct {
	RequiredVariables   []string
	ReferencedVariables []string
	HasClientTemplate   bool
	// HasLiteralText reports non-blank text outside client templates, so
	// expansion can never produce a blank value.
	HasLiteralText bool
	// MayBeEmpty conservatively reports possible exact-empty expansion.
	MayBeEmpty bool
}

// AnalyzeEnvReferences finds names without resolving their values.
func AnalyzeEnvReferences(value string) (EnvReferenceAnalysis, error) {
	masked := maskFoundryExpressions(value)
	tree, err := parse.Parse(masked)
	if err != nil {
		return EnvReferenceAnalysis{}, errors.New("invalid environment template")
	}

	required := map[string]struct{}{}
	referenced := map[string]struct{}{}
	analysis := EnvReferenceAnalysis{}
	collectEnvReferences(tree.Root, required, referenced, &analysis.HasClientTemplate)
	analysis.HasLiteralText = hasLiteralText(tree.Root)
	analysis.MayBeEmpty = mayExpandToEmpty(tree.Root)
	analysis.RequiredVariables = slices.Sorted(maps.Keys(required))
	analysis.ReferencedVariables = slices.Sorted(maps.Keys(referenced))
	return analysis, nil
}

func collectEnvReferences(
	node parse.Node,
	required map[string]struct{},
	referenced map[string]struct{},
	hasTemplate *bool,
) {
	switch typed := node.(type) {
	case *parse.FuncNode:
		*hasTemplate = true
		referenced[typed.Param] = struct{}{}
		if !hasOptionalEnvFallback(typed) {
			required[typed.Param] = struct{}{}
		}
		for _, arg := range typed.Args {
			collectEnvReferences(arg, required, referenced, hasTemplate)
		}
	case *parse.ListNode:
		for _, child := range typed.Nodes {
			collectEnvReferences(child, required, referenced, hasTemplate)
		}
	}
}

func hasLiteralText(node parse.Node) bool {
	switch typed := node.(type) {
	case *parse.TextNode:
		return strings.TrimSpace(typed.Value) != ""
	case *parse.ListNode:
		return slices.ContainsFunc(typed.Nodes, hasLiteralText)
	default:
		return false
	}
}

func hasOptionalEnvFallback(node *parse.FuncNode) bool {
	switch node.Name {
	case "=", ":=", ":-", ":+", ":?":
		return true
	default:
		return false
	}
}

func mayExpandToEmpty(node parse.Node) bool {
	switch typed := node.(type) {
	case *parse.TextNode:
		return typed.Value == ""
	case *parse.ListNode:
		for _, child := range typed.Nodes {
			if !mayExpandToEmpty(child) {
				return false
			}
		}
	case *parse.FuncNode:
		switch typed.Name {
		case "=", ":=", ":-", ":+", ":?":
			// The pinned SDK treats :+ and :? as defaults too.
			// Defaults apply when the variable is empty. Both branches
			// are nonempty if the fallback cannot expand to empty.
			for _, arg := range typed.Args {
				if !mayExpandToEmpty(arg) {
					return false
				}
			}
		}
		// Leave other transformations conservative.
	}
	return true
}

func maskFoundryExpressions(value string) string {
	spans := foundryTemplatePattern.FindAllString(value, -1)
	if len(spans) == 0 {
		return value
	}

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
	return masked
}
