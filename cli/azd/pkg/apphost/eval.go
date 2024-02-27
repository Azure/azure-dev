package apphost

import (
	"fmt"
	"strings"
)

// evalString evaluates a given string expression, using the provided evalExpr function to produce values for expressions
// in the string.  It supports strings that contain expressions of the form "{expression}" where "expression" is any string
// that does not contain a '}' character.  The evalExpr function is called with the expression (without the enclosing '{'
// and '}' characters) and should return the value to be substituted into the string.  If the evalExpr function returns
// an error, evalString will return that error. The '{' and '}' characters can be escaped by doubling them, e.g.
// "{{" and "}}". If a string is malformed (e.g. an unmatched '{' or '}' character), evalString will return an error.
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
