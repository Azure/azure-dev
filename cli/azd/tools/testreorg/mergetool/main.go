// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Command mergetool moves every top-level declaration (everything after the
// import block) from a source test file into a target test file in the same
// package. It was written to support the test reorganization described in
// https://github.com/Azure/azure-dev/issues/8799, where catch-all coverage
// files were redistributed into source-matched *_test.go files.
//
// Usage:
//
//	mergetool <from> <to>
//
// If <to> does not exist it is created with a copyright header and the package
// clause of <from>. The <from> file is left in place (the caller removes it via
// "git rm"). Imports are intentionally not reconciled here; run goimports and
// gofmt over the touched files afterwards.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
)

// bodyAfterImports returns the package name of the file at path and the raw
// source bytes that follow the package clause and any import declarations.
func bodyAfterImports(path string) (pkg string, body []byte) {
	src, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	// Cut after the package clause by default.
	cut := fset.Position(f.Name.End()).Offset
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			end := fset.Position(gd.End()).Offset
			if end > cut {
				cut = end
			}
		}
	}
	return f.Name.Name, src[cut:]
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mergetool <from> <to>")
		os.Exit(2)
	}
	from, to := os.Args[1], os.Args[2]

	pkg, body := bodyAfterImports(from)

	if _, err := os.Stat(to); os.IsNotExist(err) {
		// Create target with header + package clause; goimports adds imports later.
		header := "// Copyright (c) Microsoft Corporation. All rights reserved.\n" +
			"// Licensed under the MIT License.\n\n" +
			"package " + pkg + "\n"
		out := append([]byte(header), body...)
		if err := os.WriteFile(to, out, 0o600); err != nil {
			panic(err)
		}
		return
	}

	existing, err := os.ReadFile(to)
	if err != nil {
		panic(err)
	}
	out := append(existing, '\n')
	out = append(out, body...)
	if err := os.WriteFile(to, out, 0o600); err != nil {
		panic(err)
	}
}
