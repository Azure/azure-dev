// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

// Escape handling must match the expander that owns each field.
// Every Foundry field, including the three project network values
// (network.agentSubnet.vnet, network.peSubnet.vnet,
// network.dns.subscription), resolves through foundry.ExpandEnv and
// so takes honorEnvironmentEscaping: it collapses '$' pairs, so
// $${VAR} stays literal, and it reserves ${{...}} spans for Foundry.
// ignoreEnvironmentEscaping remains for a field owned by an expander
// without that behavior; no field takes it today.
const (
	honorEnvironmentEscaping  = true
	ignoreEnvironmentEscaping = false
)

// environmentReferencePrefix parses only the reference prefix.
// Balanced defaults remain the scanner's responsibility.
var environmentReferencePrefix = regexp.MustCompile(
	`^\$\{([A-Za-z_][A-Za-z0-9_]*)(\}|:-)`,
)

// environmentReference is one azd ${VAR} occurrence in a string.
// Start and End bound the whole reference, including any :-
// default, so a caller can resume scanning at End.
type environmentReference struct {
	Name       string
	Start      int
	End        int
	HasDefault bool
}

// findEnvironmentReferences returns the azd ${VAR} references in
// value, in order of appearance. It is the single scanner for the
// package: callers layer their own policy on the result rather
// than reimplementing discovery. init prompting skips references
// with a default because the expander supplies the fallback,
// while the generated service env block records them so the
// owning extension can re-apply the default.
//
// References the expander would not resolve are dropped: escaped
// ones and any reserved by a Foundry ${{...}} span. honorEscaping
// must match the expander that owns the field.
//
// A reference inside a :- default is not reported: nested azd
// references are unsupported by design, so ${OUTER:-${NESTED}}
// yields OUTER only. foundry.ExpandEnv still resolves NESTED at
// deploy, but nothing discovers it, so init never prompts for it
// and it gets no entry in the generated service env block. It
// then resolves only where the consumer keeps an azd environment
// fallback, and to empty where a declared env: drops it. Keep
// defaults literal.
func findEnvironmentReferences(value string, honorEscaping bool) []environmentReference {
	candidates := environmentReferenceCandidates(value, honorEscaping)
	if !honorEscaping || len(candidates) == 0 {
		return candidates
	}

	protected := protectedEnvironmentReferences(value, candidates)
	references := make([]environmentReference, 0, len(candidates))
	for i, candidate := range candidates {
		if protected[i] {
			continue
		}
		references = append(references, candidate)
	}
	if len(references) == 0 {
		return nil
	}
	return references
}

// environmentReferenceCandidates scans value left to right for
// ${NAME} and ${NAME:-default} occurrences. drone/envsubst, which
// backs foundry.ExpandEnv, collapses a '$' pair into a literal
// '$' and keeps reading, so an escape only neutralizes the '${'
// it precedes: the text after it, including a default, still
// holds live references. Membership of a ${{...}} span is left to
// findEnvironmentReferences. Scanning resumes at the end of a
// match, so a default span is never scanned again; that is what
// keeps nested references out.
func environmentReferenceCandidates(value string, honorEscaping bool) []environmentReference {
	var references []environmentReference
	for index := 0; index < len(value); {
		if value[index] != '$' {
			index++
			continue
		}
		if honorEscaping && strings.HasPrefix(value[index:], "$$") {
			index += 2
			continue
		}

		reference, found := environmentReferenceAt(value, index)
		if !found {
			index++
			continue
		}

		references = append(references, reference)
		index = reference.End
	}
	return references
}

// environmentReferenceAt parses the reference opening at start.
// The anchored prefix keeps a bare '$' from being read as one.
// Balanced defaults still need the stateful end scanner below.
func environmentReferenceAt(value string, start int) (environmentReference, bool) {
	if start < 0 || start >= len(value) {
		return environmentReference{}, false
	}

	match := environmentReferencePrefix.FindStringSubmatch(value[start:])
	if match == nil {
		return environmentReference{}, false
	}

	name := match[1]
	prefixEnd := start + len(match[0])
	if match[2] == "}" {
		return environmentReference{
			Name:  name,
			Start: start,
			End:   prefixEnd,
		}, true
	}

	end, found := environmentReferenceEnd(value, prefixEnd)
	if !found {
		return environmentReference{}, false
	}
	return environmentReference{
		Name:       name,
		Start:      start,
		End:        end,
		HasDefault: true,
	}, true
}

// environmentReferenceEnd finds the '}' closing a :- default. It
// counts nested ${...} and steps over Foundry ${{...}} spans,
// which are legal default values, so the reported span covers the
// whole reference.
func environmentReferenceEnd(value string, index int) (int, bool) {
	depth := 1
	for index < len(value) {
		if strings.HasPrefix(value[index:], "${{") {
			end := strings.Index(value[index+3:], "}}")
			if end < 0 {
				return 0, false
			}
			index += end + 5
			continue
		}
		if strings.HasPrefix(value[index:], "${") {
			depth++
			index += 2
			continue
		}
		if value[index] == '}' {
			depth--
			index++
			if depth == 0 {
				return index, true
			}
			continue
		}
		index++
	}
	return 0, false
}

// protectedEnvironmentReferences reports which candidates sit
// inside a server-side ${{...}} span. Each candidate is replaced
// with a unique probe before running [foundry.ExpandEnv]; probes
// left verbatim are reserved by the shared expander. This keeps
// discovery linked to the owning implementation without ambiguous
// name-based occurrence counting.
func protectedEnvironmentReferences(value string, references []environmentReference) []bool {
	protected := make([]bool, len(references))
	if len(references) == 0 {
		return protected
	}

	probePrefix := "AZD_ENV_REFERENCE_PROBE_"
	for strings.Contains(value, probePrefix) {
		probePrefix += "_"
	}

	probeRefs := make([]string, len(references))
	var probed strings.Builder
	last := 0
	for i, reference := range references {
		probed.WriteString(value[last:reference.Start])
		probeRefs[i] = fmt.Sprintf("${%s%d}", probePrefix, i)
		probed.WriteString(probeRefs[i])
		last = reference.End
	}
	probed.WriteString(value[last:])

	expanded, err := foundry.ExpandEnv(probed.String(), func(name string) string {
		return "expanded_" + name
	})
	if err != nil {
		return protected
	}
	for i, probeRef := range probeRefs {
		protected[i] = strings.Contains(expanded, probeRef)
	}
	return protected
}
