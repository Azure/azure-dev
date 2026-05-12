// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// bicepOutputPattern matches a Bicep top-level `output` declaration header.
// Group 1 is the output name. The header form supported is:
//
//	output <name> <type-expression> = <expression>
//
// where <type-expression> is anything between the name and the `=` sign:
// a single identifier (`string`, `int`, `bool`, `object`, `array`), a
// parameterized type (`string[]`, `string?`), a dotted alias
// (`Microsoft.Storage`), a literal-type union (`'gpt-4o' | 'gpt-4.1'`),
// or a user-defined alias. We do not validate the type expression — we
// only need to identify that this line is an output and capture its
// name. Optional leading whitespace is allowed so that indented outputs
// inside conditional / loop blocks still match. Decorators such as
// `@description('…')` live on their own line and do not interfere
// because of the line anchor.
//
// Multi-line object/array literals are accepted because we only need the
// header — once the expression begins after `=` the parser walks past the
// remainder regardless of how many lines it spans.
//
// Bicep top-level `output` declarations are written by azd's Bicep
// provider to the environment dotenv verbatim, with no case conversion
// (see cli/azd/pkg/infra/provisioning/bicep/bicep_provider.go around the
// `outputs[key] = ...` write and pkg/infra/provisioning/manager.go's
// `UpdateEnvironment` which calls `env.DotenvSet(key, ...)`). So the
// names captured here are exactly the env var names that `azd provision`
// will set in `.azure/<env>/.env`.
//
// Known limitations (acceptable for v1):
//   - String literals are not parsed: if a Bicep `var x = 'literal /*
//     ...'` opens a fake block comment, subsequent lines may be skipped.
//   - Triple-quoted multi-line strings (`”'...”'`) are not tracked:
//     embedding the literal text `output X Y = Z` inside one can produce
//     a spurious capture. azd-generated templates do not exhibit either
//     pattern in practice.
//   - `module.<m>.outputs.<x>` re-exports are not followed: only the
//     outputs `main.bicep` itself declares are written to `.env` by azd.
var bicepOutputPattern = regexp.MustCompile(`^\s*output\s+([A-Za-z_][A-Za-z0-9_]*)\s+[^=]+=`)

// bicepLineCommentPrefix is the only line-comment style Bicep supports
// (apart from `/* … */` block comments, which we handle separately).
const bicepLineCommentPrefix = "//"

// discoverBicepOutputs returns the set of top-level `output` names declared
// in <projectPath>/infra/main.bicep, sorted ascending. The names are
// returned verbatim — no case conversion is performed because azd writes
// Bicep output names to `.env` verbatim (see bicepOutputPattern doc).
//
// All failure modes (missing file, missing directory, read error, malformed
// content) return a nil slice. The caller (detectMissingVars in state.go)
// treats an empty Bicep-output set as "no infra-classified vars" and
// routes every unresolved `${VAR}` reference into the manual-vars bucket.
// There is no prefix-based fallback; the deterministic Bicep-output set
// is the only source of truth, matching issue #7975 State Inputs line 74.
//
// We deliberately do not resolve `module.<m>.outputs.<x>` re-exports: only
// outputs that the top-level `main.bicep` exposes are written to the
// environment file by azd. If a downstream module declares an output but
// it is not surfaced at the root, azd will not write it to `.env`.
//
// Block comments (`/* … */`) and single-line comments (`// …`) are
// stripped before matching so that a commented-out `// output foo …` is
// not picked up. Block comments are supported even when they span
// multiple lines.
func discoverBicepOutputs(projectPath string) []string {
	if projectPath == "" {
		return nil
	}

	bicepPath := filepath.Join(projectPath, "infra", "main.bicep")
	//nolint:gosec // G304: path is derived from the azd project root, not user input.
	file, err := os.Open(bicepPath)
	if err != nil {
		// Best-effort: missing file or any read error returns an empty
		// set. The caller treats an empty Bicep-output set as "no
		// infra-classified vars" and routes every unresolved ${VAR}
		// reference into the manual-vars bucket. We don't distinguish
		// fs.ErrNotExist from permission / I/O errors here; the
		// classifier deliberately does not block on Bicep parse
		// problems.
		return nil
	}
	defer file.Close()

	return parseBicepOutputs(file)
}

// parseBicepOutputs walks the given reader line-by-line, stripping comments
// and matching the output header pattern. Split out from discoverBicepOutputs
// so tests can drive it without writing to disk.
func parseBicepOutputs(r io.Reader) []string {
	scanner := bufio.NewScanner(r)
	// main.bicep files in real templates routinely exceed bufio's default
	// 64 KiB scanner line limit when single-line outputs reference deeply
	// nested module expressions; raise the cap to 1 MiB so we do not
	// silently miss outputs near the end of a long file.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	seen := make(map[string]struct{})
	inBlockComment := false

	for scanner.Scan() {
		line := scanner.Text()

		// Strip in-flight or starting block comments. We track block-comment
		// state across lines because Bicep allows `/* … */` to span
		// multiple lines.
		stripped, stillInBlock := stripBicepComments(line, inBlockComment)
		inBlockComment = stillInBlock

		// Skip the line if, after stripping, it begins with a line comment
		// (very rare to see `// output foo …` but possible in templates).
		trimmed := strings.TrimSpace(stripped)
		if trimmed == "" || strings.HasPrefix(trimmed, bicepLineCommentPrefix) {
			continue
		}

		match := bicepOutputPattern.FindStringSubmatch(stripped)
		if match == nil {
			continue
		}
		seen[match[1]] = struct{}{}
	}
	// scanner.Err() (e.g., bufio.ErrTooLong on a >1 MiB line) is
	// intentionally not surfaced: this is a best-effort classifier and a
	// partial parse is more useful than nil. Any outputs successfully
	// captured before a scan error still route to infra correctly,
	// instead of silently falling back to the fully-manual default.
	return sortedKeys(seen)
}

func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// stripBicepComments removes `// …` line comments and `/* … */` block
// comments from a single line, given the inBlock state carried over from
// previous lines. Returns the stripped text and the new inBlock state.
// The implementation is intentionally simple: it does not honor comment
// markers that appear inside string literals because Bicep's comment
// syntax is restricted and the output-pattern regex requires a leading
// `output` keyword that cannot appear inside a string literal context
// before the `=` anyway.
func stripBicepComments(line string, inBlock bool) (string, bool) {
	var b strings.Builder
	i := 0
	for i < len(line) {
		if inBlock {
			// Look for the end of the block comment on this line.
			end := strings.Index(line[i:], "*/")
			if end < 0 {
				// Block comment continues past end of line.
				return b.String(), true
			}
			i += end + 2
			inBlock = false
			continue
		}
		// Not in a block comment: look for the next comment opener.
		if i+1 < len(line) {
			pair := line[i : i+2]
			if pair == "/*" {
				inBlock = true
				i += 2
				continue
			}
			if pair == bicepLineCommentPrefix {
				// Rest of line is a line comment.
				return b.String(), false
			}
		}
		b.WriteByte(line[i])
		i++
	}
	return b.String(), inBlock
}
