// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

// EnvReference is one azd ${VAR} occurrence in a string. Start and End bound the
// whole reference, including any :- default, so a caller can resume scanning at
// End.
type EnvReference struct {
	Name       string
	Start      int
	End        int
	HasDefault bool
}

// envReferencePrefix parses only the reference prefix. Balanced defaults remain
// the scanner's responsibility.
var envReferencePrefix = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)(\}|:-)`)

// FindEnvReferences returns the azd ${VAR} references in value that
// [foundry.ExpandEnv] actually resolves, in order of appearance.
//
// This is the single scanner for azd references in a Foundry value. Every
// consumer layers its own policy on the result rather than reimplementing
// discovery: init prompting skips references with a default because the
// expander supplies the fallback, the generated service env block records them
// so the owning extension can re-apply the default, and resolveVars treats the
// ones without a default as the names that must resolve. Re-deriving the escape
// and ${{...}} rules per consumer is what lets them drift away from the
// expander.
//
// References the expander would not resolve are dropped: escaped ones, and any
// reserved by a Foundry ${{...}} span.
//
// A reference inside a :- default is not reported: nested azd references are
// unsupported by design, so ${OUTER:-${NESTED}} yields OUTER only.
// [foundry.ExpandEnv] still resolves NESTED at deploy, but nothing discovers it,
// so init never prompts for it and it gets no entry in the generated service env
// block. It then resolves only where the consumer keeps an azd environment
// fallback, and to empty where a declared env: drops it. Keep defaults literal.
func FindEnvReferences(value string) []EnvReference {
	candidates := envReferenceCandidates(value)
	if len(candidates) == 0 {
		return nil
	}

	protected := protectedEnvReferences(value, candidates)
	references := make([]EnvReference, 0, len(candidates))
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

// ValidateEnvReferences reports an error when value carries a '$' form that
// [foundry.ExpandEnv] would act on but [FindEnvReferences] does not report.
//
// drone/envsubst, which backs the expander, implements the full shell parameter
// grammar: ${VAR:=default}, ${VAR:+alt}, ${VAR:?message}, ${VAR#prefix} and
// ${VAR:0:3} all expand. None of them are shapes the scanner above reports, so
// without this check they slip past the unresolved-variable guard and quietly
// rewrite a value that the caller then validates as if the user had written it.
// Typing ':=' instead of ':-' is a one character slip that would otherwise
// succeed while skipping the very guard it looks like it is using.
//
// A reference nested in a :- default is refused too — and that is a shape which
// resolves correctly today whenever the nested name is set:
// ${VNET_ID:-${FALLBACK_VNET_ID}} works on a project that has FALLBACK_VNET_ID
// in its environment. It is withdrawn rather than fixed because `required` is
// computed statically, and whether the nested name has to resolve depends on
// whether the outer one does — which is not known when the value is scanned.
// Reporting the nested name would raise a false unresolved-variable error every
// time the outer name IS set; not reporting it leaves ${A:-${B}} with neither
// set expanding to empty, so the caller blames the empty value instead of naming
// B. Neither half is right, so the shape goes.
//
// Where this runs, it makes [FindEnvReferences] complete: every occurrence the
// expander acts on is one the scanner saw. That is a property of the *call*,
// not of the scanner — only callers that invoke this get it. Today that is the
// three project network fields (network.agentSubnet.vnet, network.peSubnet.vnet,
// network.dns.subscription). Discovery-only consumers, such as init prompting
// and the generated service env block, still scan values that were never
// validated; extending the check to them is tracked by
// https://github.com/Azure/azure-dev/issues/9428.
func ValidateEnvReferences(value string) error {
	// Non-zero while scanning the inside of a :- default. Nesting is refused on
	// sight, so one boundary is enough — there is never a second level.
	defaultEnd := 0
	for index := 0; index < len(value); {
		if defaultEnd > 0 && index >= defaultEnd {
			defaultEnd = 0
		}
		if value[index] != '$' {
			index++
			continue
		}
		// Outside a default, a '$' pair collapses to a literal '$',
		// neutralizing only the '${' it precedes. Inside a :- default,
		// envsubst re-parses the default text and the second '$' can still open
		// a live ${VAR}, so leave it for the nested-reference check below.
		// A Foundry span is masked before envsubst sees the pair in either case.
		if defaultEnd == 0 &&
			strings.HasPrefix(value[index:], "$$") &&
			!strings.HasPrefix(value[index+1:], "${{") {
			index += 2
			continue
		}
		// A Foundry span is reserved verbatim for the service to resolve. Legal
		// as a default value, so this stays allowed inside one.
		if strings.HasPrefix(value[index:], "${{") {
			end := strings.Index(value[index+3:], "}}")
			if end < 0 {
				return fmt.Errorf("%q is missing the closing }} of a Foundry expression",
					unsupportedEnvFragment(value, index))
			}
			index += end + 5
			continue
		}
		// A bare '$' is not a reference: envsubst expands only the braced form,
		// so "$VAR" and "costs $5" survive expansion untouched.
		if !strings.HasPrefix(value[index:], "${") {
			index++
			continue
		}
		reference, found := envReferenceAt(value, index)
		if defaultEnd > 0 && found {
			return fmt.Errorf(
				"%q nests an environment variable reference inside a :- default, which azd "+
					"cannot check: whether the nested name is required depends on whether the "+
					"outer one resolves, and that is not known when the value is scanned. Use a "+
					"single ${VAR} and set it in the azd environment, or give the default a "+
					"literal value",
				value[reference.Start:reference.End])
		}
		if !found {
			return fmt.Errorf(
				"%q is not a supported environment variable reference; use ${VAR} or "+
					"${VAR:-default}, $${VAR} to keep it literal, or ${{...}} for a Foundry expression",
				unsupportedEnvFragment(value, index))
		}
		if reference.HasDefault {
			// Step into the default rather than over it. FindEnvReferences stops
			// at the default because nested references are not *discovered*;
			// envsubst still *expands* whatever is in there, so both an
			// unsupported form and a nested reference have to be caught here.
			defaultEnd = reference.End
			index = reference.Start + len("${") + len(reference.Name) + len(":-")
			continue
		}
		index = reference.End
	}
	return nil
}

// unsupportedEnvFragment returns the reference-looking fragment starting at
// index so an error can quote the offending text rather than the whole value. It
// stops at the first '}' because an unsupported form is by definition one the
// span scanner cannot bound.
func unsupportedEnvFragment(value string, index int) string {
	rest := value[index:]
	if end := strings.IndexByte(rest, '}'); end >= 0 {
		return rest[:end+1]
	}
	return rest
}

// envReferenceCandidates scans value left to right for ${NAME} and
// ${NAME:-default} occurrences. drone/envsubst, which backs
// [foundry.ExpandEnv], collapses a '$' pair into a literal '$' and keeps
// reading, so an escape only neutralizes the '${' it precedes: the text after
// it, including a default, still holds live references. Membership of a ${{...}}
// span is left to [FindEnvReferences]. Scanning resumes at the end of a match,
// so a default span is never scanned again; that is what keeps nested references
// out.
func envReferenceCandidates(value string) []EnvReference {
	var references []EnvReference
	for index := 0; index < len(value); {
		if value[index] != '$' {
			index++
			continue
		}
		if strings.HasPrefix(value[index:], "$$") {
			index += 2
			continue
		}

		reference, found := envReferenceAt(value, index)
		if !found {
			index++
			continue
		}

		references = append(references, reference)
		index = reference.End
	}
	return references
}

// envReferenceAt parses the reference opening at start. The anchored prefix
// keeps a bare '$' from being read as one. Balanced defaults still need the
// stateful end scanner below.
func envReferenceAt(value string, start int) (EnvReference, bool) {
	if start < 0 || start >= len(value) {
		return EnvReference{}, false
	}

	match := envReferencePrefix.FindStringSubmatch(value[start:])
	if match == nil {
		return EnvReference{}, false
	}

	name := match[1]
	prefixEnd := start + len(match[0])
	if match[2] == "}" {
		return EnvReference{
			Name:  name,
			Start: start,
			End:   prefixEnd,
		}, true
	}

	end, found := envReferenceEnd(value, prefixEnd)
	if !found {
		return EnvReference{}, false
	}
	return EnvReference{
		Name:       name,
		Start:      start,
		End:        end,
		HasDefault: true,
	}, true
}

// envReferenceEnd finds the '}' closing a :- default. It counts nested ${...}
// and steps over Foundry ${{...}} spans, which are legal default values, so the
// reported span covers the whole reference.
func envReferenceEnd(value string, index int) (int, bool) {
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

// protectedEnvReferences reports which candidates sit inside a server-side
// ${{...}} span. Each candidate is replaced with a unique probe before running
// [foundry.ExpandEnv]; probes left verbatim are reserved by the shared expander.
// This keeps discovery linked to the owning implementation without ambiguous
// name-based occurrence counting.
func protectedEnvReferences(value string, references []EnvReference) []bool {
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
