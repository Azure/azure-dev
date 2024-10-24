// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"unicode"
)

type location struct {
	start int
	stop  int
}

// parseEnvSubstVariables parses the envsubst expression(s) present in a string.
// substitutions, returning the locations of the expressions and the names of the variables.
//
// It works with both:
//   - ${var} and
//   - ${var:=default} syntaxes
func parseEnvSubstVariables(s string) (names []string, expressions []location) {
	inVar := false
	inVarName := false
	name := strings.Builder{}

	i := 0
	start := 0 // start of the variable expression
	for i < len(s) {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' { // detect ${ sequence
			inVar = true
			inVarName = true
			start = i
			i += len("${")
			continue
		}

		if inVar {
			if inVarName { // detect the end of the variable name
				// a variable name can contain letters, digits, and underscores, and nothing else.
				if unicode.IsLetter(rune(s[i])) || unicode.IsDigit(rune(s[i])) || s[i] == '_' {
					_ = name.WriteByte(s[i])
				} else { // a non-matching character means we've reached the end of the name
					inVarName = false
				}
			}

			if s[i] == '}' { // detect the end of the variable expression
				inVar = false
				names = append(names, name.String())
				name.Reset()
				expressions = append(expressions, location{start, i})
			}
		}

		i++
	}
	return
}
