// find_string_constructors.go
package main

import (
    "go/parser"
    "go/token"
    "go/ast"
    "os"
    "path/filepath"
    "fmt"
    "strings"
)

func main() {
    root := "."
    err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
        if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
            return nil
        }
        fset := token.NewFileSet()
        node, err := parser.ParseFile(fset, path, nil, 0)
        if err != nil {
            return nil
        }
        for _, decl := range node.Decls {
            if fn, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "New") {
                for _, param := range fn.Type.Params.List {
                    if ident, ok := param.Type.(*ast.Ident); ok && ident.Name == "string" {
                        fmt.Printf("%s: %s\n", path, fn.Name.Name)
                    }
                }
            }
        }
        return nil
    })
    if err != nil {
        fmt.Println("Error:", err)
    }
}