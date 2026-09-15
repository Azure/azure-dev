// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// A printed command is a suggestion the reader is meant to paste. Anything
// interpolated into one therefore reaches a shell, and the values here are not
// the command's own: dataset and evaluator names come from the configuration or
// from the service's listing, run and job ids come back over the wire. shellArg
// exists for exactly that, and the way this stops being true is not somebody
// deleting it -- it is the next message being written without it.
//
// So the guard is on the call sites rather than on shellArg. A test that hands
// shellArg a hostile string and checks the result would keep passing while a new
// `azd ...%s` went in raw beside it, which is the failure that actually happens.
func TestEveryPrintedCommandQuotesWhatItInterpolates(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "messages.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing messages.go: %v", err)
	}

	var unquoted []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isFormatter(call.Fun) || len(call.Args) == 0 {
				return true
			}
			format, ok := constantString(call.Args[0])
			if !ok {
				return true
			}
			args := call.Args[1:]
			for _, verb := range commandVerbs(format) {
				if verb >= len(args) {
					continue
				}
				if sanitized(args[verb], fn) {
					continue
				}
				unquoted = append(unquoted, fmt.Sprintf(
					"%s (%s): argument %d is interpolated into a printed command unquoted",
					fn.Name.Name, fset.Position(args[verb].Pos()), verb+1))
			}
			return true
		})
		return true
	})

	for _, u := range unquoted {
		t.Errorf("%s\n\twrap it in shellArg, so a name carrying shell syntax "+
			"cannot run when the line is pasted", u)
	}
}

// isFormatter reports the fmt calls that take a format string first.
func isFormatter(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" {
		return false
	}
	switch sel.Sel.Name {
	case "Errorf", "Sprintf":
		return true
	}
	return false
}

// constantString rebuilds a format string written as concatenated literals,
// which is how the long ones in this file are spelled.
func constantString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		left, ok := constantString(v.X)
		if !ok {
			return "", false
		}
		right, ok := constantString(v.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

// commandVerbs returns the argument indexes whose verb falls inside a command
// the reader is invited to paste. Every verb is counted, because arguments are
// positional and a %q earlier in the string still consumes one.
func commandVerbs(format string) []int {
	pasteable := commandMask(format)

	var inCommand []int
	arg := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		if i+1 < len(format) && format[i+1] == '%' {
			i++
			continue
		}
		j := i + 1
		for j < len(format) && strings.ContainsRune("+-# 0123456789.", rune(format[j])) {
			j++
		}
		if j >= len(format) {
			continue
		}
		if pasteable[i] {
			inCommand = append(inCommand, arg)
		}
		arg++
		i = j
	}
	return inCommand
}

// commandMask marks the bytes of format that sit inside a printed command.
//
// Both spellings count. Backticks are the common one, but the longer
// suggestions are set out on their own indented line instead, and a command is
// no less pasteable for being printed that way -- which is the shape this guard
// missed when it looked only for backticks.
func commandMask(format string) []bool {
	mask := make([]bool, len(format))

	for i := 0; i < len(format); i++ {
		if format[i] != '`' {
			continue
		}
		end := strings.IndexByte(format[i+1:], '`')
		if end < 0 {
			break
		}
		end += i + 1
		if strings.HasPrefix(format[i+1:], "azd ") {
			for k := i + 1; k < end; k++ {
				mask[k] = true
			}
		}
		i = end
	}

	at := 0
	for line := range strings.SplitSeq(format, "\n") {
		if trimmed := strings.TrimLeft(line, " \t"); strings.HasPrefix(trimmed, "azd ") {
			for k := at + len(line) - len(trimmed); k < at+len(line); k++ {
				mask[k] = true
			}
		}
		at += len(line) + 1
	}
	return mask
}

// sanitized reports an expression that cannot deliver shell syntax: a call to
// shellArg, a constant this file wrote itself, or a local built only out of
// those. A parameter on its own is not, however it was spelled at the caller.
func sanitized(e ast.Expr, fn *ast.FuncDecl) bool {
	switch v := e.(type) {
	case *ast.CallExpr:
		switch f := v.Fun.(type) {
		case *ast.Ident:
			return f.Name == "shellArg" || f.Name == "ShellArg"
		case *ast.SelectorExpr:
			return f.Sel.Name == "ShellArg"
		}
		return false
	case *ast.BasicLit:
		return true
	case *ast.BinaryExpr:
		return v.Op == token.ADD && sanitized(v.X, fn) && sanitized(v.Y, fn)
	case *ast.Ident:
		// A local assembled inside this function is safe when every value it is
		// ever given is. OutputItemRequired builds its suggestion that way.
		if isParameter(v.Name, fn) {
			return false
		}
		assigned := false
		safe := true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range assign.Lhs {
				name, ok := lhs.(*ast.Ident)
				if !ok || name.Name != v.Name || i >= len(assign.Rhs) {
					continue
				}
				assigned = true
				if !sanitized(assign.Rhs[i], fn) {
					safe = false
				}
			}
			return true
		})
		return assigned && safe
	}
	return false
}

// isParameter reports a name that arrived from the caller.
func isParameter(name string, fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		for _, ident := range field.Names {
			if ident.Name == name {
				return true
			}
		}
	}
	return false
}
