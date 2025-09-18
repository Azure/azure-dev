// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package apphost

import (
	"errors"
	"fmt"
	"log"
	"strings"
)

type UnrecognizedExpressionError struct {
}

func (e UnrecognizedExpressionError) Error() string {
	return "not recognized expression"
}

// evalString evaluates a given string expression, using the provided evalExpr function to produce values for expressions
// in the string.  It supports strings that contain expressions of the form "{expression}" where "expression" is any string
// that does not contain a '}' character.  The evalExpr function is called with the expression (without the enclosing '{'
// and '}' characters) and should return the value to be substituted into the string.  If the evalExpr function returns
// an error, evalString will return that error. The '{' and '}' characters can be escaped by doubling them, e.g.
// "{{" and "}}". If a string is malformed (e.g. an unmatched '{' or '}' character), evalString will return an error.
// evalExpr can return error: UnrecognizedExpressionError to indicate that the expression could not be recognized. This
// means there was not an error evaluating the expression, but rather that the evalExpr function has no idea how to handle
// such expression. In this case, evalString will consider the expression as if it was skipped with '{{' and '}}' characters,
// returning the full expression as is. For example, finding "{unknown}" will return "{unknown}" as part of the result.
func EvalString(src string, evalExpr func(string) (string, error)) (string, error) {
	var res strings.Builder

	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '{':
			if i+1 < len(src) && src[i+1] == '{' {
				res.WriteByte('{')
				i++
				continue
			}

			closed := false
			for j := i + 1; j < len(src); j++ {
				if src[j] == '}' {
					v, err := evalExpr(src[i+1 : j])
					if errors.Is(err, UnrecognizedExpressionError{}) {
						exp := src[i : j+1]
						log.Printf("Skipping unrecognized expression: %s\n", exp)
						v = exp   // Use the original expression as is
						err = nil // Reset error to nil since we are not failing
					}
					if err != nil {
						return "", err
					}

					res.WriteString(v)
					i = j
					closed = true
					break
				}
			}

			if !closed {
				return "", fmt.Errorf("unclosed '{' at position %d", i)
			}
		case '}':
			if i+1 < len(src) && src[i+1] == '}' {
				res.WriteByte('}')
				i++
				continue
			}
			return "", fmt.Errorf("unexpected '}' at position %d", i)
		default:
			res.WriteByte(src[i])
		}
	}

	return res.String(), nil
}
