// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Command router redistributes the top-level declarations of a catch-all test
// file into per-source-file *_test.go files within the same package directory.
// It was written to support the test reorganization described in
// https://github.com/Azure/azure-dev/issues/8799.
//
// Each declaration is assigned to the non-test source file that defines the
// most symbols the declaration references. Declarations are moved verbatim
// (including doc comments) so behavior and coverage are unchanged. Because all
// destinations live in the same package, compilation is preserved by
// construction. Imports are reconciled separately with goimports and gofmt.
//
// Usage:
//
//	router -dry <catchall_test.go>     # print decl -> destination plan
//	router -apply <catchall_test.go>   # perform the moves and delete the input
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type decl struct {
	name   string
	start  int // byte offset including doc comment
	end    int
	isTest bool
	dest   string // destination base (without _test.go)
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: router [-dry|-apply] <file>")
		os.Exit(2)
	}
	mode := os.Args[1]
	path := os.Args[2]
	dir := filepath.Dir(path)
	inputBase := strings.TrimSuffix(filepath.Base(path), ".go")

	symFile := buildSymbolMap(dir)

	src, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	cf, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	tf := fset.File(cf.Pos())

	decls := collectDecls(cf, tf)
	scoreDecls(decls, cf, tf, symFile)

	// Fallback for decls with no symbol match: route to the most common
	// destination chosen by sibling decls in this file, else a stable default.
	def := defaultDest(decls, inputBase)
	for _, dc := range decls {
		if dc.dest == "" {
			dc.dest = def
		}
	}

	if mode == "-dry" {
		printPlan(decls)
		return
	}
	applyMoves(decls, src, dir, cf.Name.Name)
	if err := os.Remove(path); err != nil {
		panic(err)
	}
}

// buildSymbolMap maps each top-level symbol defined in the non-test source
// files of dir to the base name (without ".go") of the file that defines it.
// Symbols defined in more than one file are treated as ambiguous and dropped.
func buildSymbolMap(dir string) map[string]string {
	symFile := map[string]string{}
	ambiguous := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	fset := token.NewFileSet()
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		base := strings.TrimSuffix(n, ".go")
		src, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		f, err := parser.ParseFile(fset, n, src, 0)
		if err != nil {
			continue
		}
		add := func(name string) {
			if name == "" || name == "_" {
				return
			}
			if existing, ok := symFile[name]; ok && existing != base {
				ambiguous[name] = true
				return
			}
			symFile[name] = base
		}
		for _, d := range f.Decls {
			switch g := d.(type) {
			case *ast.FuncDecl:
				add(g.Name.Name)
				if g.Recv != nil && len(g.Recv.List) > 0 {
					add(recvTypeName(g.Recv.List[0].Type))
				}
			case *ast.GenDecl:
				for _, s := range g.Specs {
					switch sp := s.(type) {
					case *ast.TypeSpec:
						add(sp.Name.Name)
					case *ast.ValueSpec:
						for _, id := range sp.Names {
							add(id.Name)
						}
					}
				}
			}
		}
	}
	for a := range ambiguous {
		delete(symFile, a)
	}
	return symFile
}

// collectDecls returns one decl entry per non-import top-level declaration in
// cf, capturing the byte range (including any doc comment) of each.
func collectDecls(cf *ast.File, tf *token.File) []*decl {
	var decls []*decl
	for _, d := range cf.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			continue
		}
		name := declName(d)
		start := tf.Offset(d.Pos())
		if dcc := docOf(d); dcc != nil {
			start = tf.Offset(dcc.Pos())
		}
		end := tf.Offset(d.End())
		isTest := false
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil &&
			(strings.HasPrefix(fd.Name.Name, "Test") || strings.HasPrefix(fd.Name.Name, "Benchmark") ||
				strings.HasPrefix(fd.Name.Name, "Example") || strings.HasPrefix(fd.Name.Name, "Fuzz")) {
			isTest = true
		}
		decls = append(decls, &decl{name: name, start: start, end: end, isTest: isTest})
	}
	return decls
}

// scoreDecls assigns each decl a destination by counting how many of the
// symbols it references are defined in each candidate source file.
func scoreDecls(decls []*decl, cf *ast.File, tf *token.File, symFile map[string]string) {
	for _, dc := range decls {
		scores := map[string]int{}
		for _, d := range cf.Decls {
			if declName(d) == dc.name && tf.Offset(d.End()) == dc.end {
				ast.Inspect(d, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok {
						if file, ok := symFile[id.Name]; ok {
							scores[file]++
						}
					}
					return true
				})
				break
			}
		}
		dc.dest = pickDest(dc.name, scores)
	}
}

// printPlan writes the decl -> destination assignment plus a per-destination
// count summary, for use with the -dry flag.
func printPlan(decls []*decl) {
	counts := map[string]int{}
	for _, dc := range decls {
		counts[dc.dest]++
		fmt.Printf("  %-50s -> %s_test.go\n", dc.name, dc.dest)
	}
	fmt.Println("  --- destination summary ---")
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %3d -> %s_test.go\n", counts[k], k)
	}
}

// applyMoves appends each decl's verbatim source text to its destination
// *_test.go file, creating the file with a header when it does not yet exist.
func applyMoves(decls []*decl, src []byte, dir, pkgName string) {
	byDest := map[string][]*decl{}
	var destOrder []string
	for _, dc := range decls {
		if _, seen := byDest[dc.dest]; !seen {
			destOrder = append(destOrder, dc.dest)
		}
		byDest[dc.dest] = append(byDest[dc.dest], dc)
	}
	for _, dest := range destOrder {
		destPath := filepath.Join(dir, dest+"_test.go")
		var b strings.Builder
		for _, dc := range byDest[dest] {
			b.Write(src[dc.start:dc.end])
			b.WriteString("\n\n")
		}
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			header := "// Copyright (c) Microsoft Corporation. All rights reserved.\n" +
				"// Licensed under the MIT License.\n\n" +
				"package " + pkgName + "\n\n"
			if err := os.WriteFile(destPath, []byte(header+b.String()), 0o600); err != nil {
				panic(err)
			}
		} else {
			existing, err := os.ReadFile(destPath)
			if err != nil {
				panic(err)
			}
			out := append(existing, '\n')
			out = append(out, []byte(b.String())...)
			if err := os.WriteFile(destPath, out, 0o600); err != nil {
				panic(err)
			}
		}
	}
}

// pickDest returns the highest-scoring destination. Ties are broken in favor of
// the source file whose base name appears in the test name, then alphabetically.
func pickDest(testName string, scores map[string]int) string {
	best := ""
	bestN := 0
	var tied []string
	for f, n := range scores {
		if n > bestN {
			bestN = n
			best = f
			tied = []string{f}
		} else if n == bestN {
			tied = append(tied, f)
		}
	}
	if bestN == 0 {
		return ""
	}
	if len(tied) > 1 {
		lower := strings.ToLower(testName)
		sort.Strings(tied)
		for _, t := range tied {
			if strings.Contains(lower, strings.ReplaceAll(t, "_", "")) {
				return t
			}
		}
		return tied[0]
	}
	return best
}

// defaultDest chooses a fallback destination for decls with no symbol match:
// the most common destination among siblings, else a name derived from the
// catch-all file's own base name.
func defaultDest(decls []*decl, inputBase string) string {
	counts := map[string]int{}
	for _, dc := range decls {
		if dc.dest != "" {
			counts[dc.dest]++
		}
	}
	best := ""
	bestN := 0
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if counts[k] > bestN {
			bestN = counts[k]
			best = k
		}
	}
	if best != "" {
		return best
	}
	b := inputBase
	for _, suf := range []string{"_coverage3_test", "_coverage_test", "_coverage2_test", "_test"} {
		b = strings.TrimSuffix(b, suf)
	}
	b = strings.TrimRight(b, "0123456789")
	return b
}

func recvTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	case *ast.IndexListExpr:
		return recvTypeName(t.X)
	}
	return ""
}

func declName(d ast.Decl) string {
	switch g := d.(type) {
	case *ast.FuncDecl:
		if g.Recv != nil && len(g.Recv.List) > 0 {
			return recvTypeName(g.Recv.List[0].Type) + "." + g.Name.Name
		}
		return g.Name.Name
	case *ast.GenDecl:
		for _, s := range g.Specs {
			switch sp := s.(type) {
			case *ast.TypeSpec:
				return "type:" + sp.Name.Name
			case *ast.ValueSpec:
				if len(sp.Names) > 0 {
					return "var:" + sp.Names[0].Name
				}
			}
		}
	}
	return "?"
}

func docOf(d ast.Decl) *ast.CommentGroup {
	switch g := d.(type) {
	case *ast.FuncDecl:
		return g.Doc
	case *ast.GenDecl:
		return g.Doc
	}
	return nil
}
