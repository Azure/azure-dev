// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// generateadapters creates beta gRPC service adapters backed by stable service implementations.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type methodKind int

const (
	unaryMethod methodKind = iota
	bidiStreamingMethod
)

type method struct {
	name     string
	kind     methodKind
	request  string
	response string
}

type service struct {
	name    string
	methods []method
}

func main() {
	stableSource := flag.String("stable-source", "", "directory containing stable generated contracts")
	betaSource := flag.String("beta-source", "", "directory containing beta generated contracts")
	output := flag.String("output", "", "path for the generated adapter file")
	flag.Parse()

	if *stableSource == "" || *betaSource == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "-stable-source, -beta-source, and -output are required")
		os.Exit(2)
	}

	if err := generate(*stableSource, *betaSource, *output); err != nil {
		fmt.Fprintf(os.Stderr, "generate adapters: %v\n", err)
		os.Exit(1)
	}
}

func generate(stableSource, betaSource, output string) error {
	stable, err := parseServices(stableSource)
	if err != nil {
		return fmt.Errorf("parse stable services: %w", err)
	}
	beta, err := parseServices(betaSource)
	if err != nil {
		return fmt.Errorf("parse beta services: %w", err)
	}

	generated, err := generateCode(stable, beta)
	if err != nil {
		return err
	}
	//nolint:gosec // Generated Go sources must retain normal repository-readable permissions.
	if err := os.WriteFile(output, generated, 0o644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

func parseServices(source string) (map[string]service, error) {
	files, err := filepath.Glob(filepath.Join(source, "*_grpc.pb.go"))
	if err != nil {
		return nil, fmt.Errorf("list generated gRPC files: %w", err)
	}
	slices.Sort(files)

	services := map[string]service{}
	fileSet := token.NewFileSet()
	for _, file := range files {
		parsed, err := parser.ParseFile(fileSet, file, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}

		for _, declaration := range parsed.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok ||
					!strings.HasSuffix(typeSpec.Name.Name, "Server") ||
					strings.HasPrefix(typeSpec.Name.Name, "Unsafe") {
					continue
				}
				serverInterface, ok := typeSpec.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}

				name := strings.TrimSuffix(typeSpec.Name.Name, "Server")
				if _, exists := services[name]; exists {
					return nil, fmt.Errorf("duplicate service interface %s", name)
				}

				methods, err := parseMethods(name, serverInterface)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", file, err)
				}
				services[name] = service{name: name, methods: methods}
			}
		}
	}

	return services, nil
}

func parseMethods(serviceName string, serverInterface *ast.InterfaceType) ([]method, error) {
	var methods []method
	for _, field := range serverInterface.Methods.List {
		if len(field.Names) != 1 || !field.Names[0].IsExported() {
			continue
		}
		function, ok := field.Type.(*ast.FuncType)
		if !ok {
			return nil, fmt.Errorf("%s.%s is not a method", serviceName, field.Names[0].Name)
		}

		parsed, err := parseMethod(field.Names[0].Name, function)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", serviceName, field.Names[0].Name, err)
		}
		methods = append(methods, parsed)
	}
	return methods, nil
}

func parseMethod(name string, function *ast.FuncType) (method, error) {
	parameters := function.Params.List
	results := function.Results.List
	if len(parameters) == 2 && len(results) == 2 {
		request, ok := pointerIdentifier(parameters[1].Type)
		if !ok {
			return method{}, fmt.Errorf("unsupported unary request type")
		}
		response, ok := pointerIdentifier(results[0].Type)
		if !ok || !isError(results[1].Type) {
			return method{}, fmt.Errorf("unsupported unary result type")
		}
		return method{name: name, kind: unaryMethod, request: request, response: response}, nil
	}

	if len(parameters) == 1 && len(results) == 1 && isError(results[0].Type) {
		request, response, ok := bidiTypes(parameters[0].Type)
		if !ok {
			return method{}, fmt.Errorf("only bidirectional generated streams are supported")
		}
		return method{name: name, kind: bidiStreamingMethod, request: request, response: response}, nil
	}

	return method{}, fmt.Errorf("unsupported generated method signature")
}

func pointerIdentifier(expression ast.Expr) (string, bool) {
	pointer, ok := expression.(*ast.StarExpr)
	if !ok {
		return "", false
	}
	identifier, ok := pointer.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return identifier.Name, true
}

func bidiTypes(expression ast.Expr) (string, string, bool) {
	generic, ok := expression.(*ast.IndexListExpr)
	if !ok || len(generic.Indices) != 2 {
		return "", "", false
	}
	selector, ok := generic.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "BidiStreamingServer" {
		return "", "", false
	}
	request, requestOK := generic.Indices[0].(*ast.Ident)
	response, responseOK := generic.Indices[1].(*ast.Ident)
	if !requestOK || !responseOK {
		return "", "", false
	}
	return request.Name, response.Name, true
}

func isError(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "error"
}

func generateCode(stable, beta map[string]service) ([]byte, error) {
	serviceNames := mapsKeys(beta)
	slices.Sort(serviceNames)
	if len(serviceNames) == 0 {
		return nil, fmt.Errorf("no beta services found")
	}

	for name, stableService := range stable {
		betaService, ok := beta[name]
		if !ok {
			return nil, fmt.Errorf("stable service %s is missing from beta", name)
		}
		for _, stableMethod := range stableService.methods {
			betaMethod, ok := findMethod(betaService, stableMethod.name)
			if !ok {
				return nil, fmt.Errorf("stable method %s.%s is missing from beta", name, stableMethod.name)
			}
			if stableMethod.kind != betaMethod.kind {
				return nil, fmt.Errorf("method stream shape differs for %s.%s", name, stableMethod.name)
			}
			if stableMethod.request != betaMethod.request || stableMethod.response != betaMethod.response {
				return nil, fmt.Errorf("request or response type differs for %s.%s", name, stableMethod.name)
			}
		}
	}

	var generated bytes.Buffer
	generated.WriteString(`// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Code generated by grpc/generateadapters. DO NOT EDIT.

package grpcserver

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	v1 "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

const (
`)
	for _, name := range serviceNames {
		fmt.Fprintf(
			&generated,
			"\t// Beta%s identifies the beta %s registration and its focused overrides.\n",
			name,
			name,
		)
		fmt.Fprintf(&generated, "\tBeta%s BetaService = %q\n", name, name)
	}
	generated.WriteString(")\n\n")

	for _, name := range serviceNames {
		betaService := beta[name]
		for _, betaMethod := range betaService.methods {
			writeOverrideInterface(&generated, betaService, betaMethod)
		}
		writeOverrideValidator(&generated, betaService)
	}

	generated.WriteString(`func registerBetaServices(
	registrar grpc.ServiceRegistrar,
	stableServices map[BetaService]any,
	overrides map[BetaService]any,
) error {
	for service := range overrides {
		switch service {
`)
	for _, name := range serviceNames {
		fmt.Fprintf(&generated, "\t\tcase Beta%s:\n", name)
	}
	generated.WriteString(`		default:
			return fmt.Errorf("unknown beta service override %q", service)
		}
	}

`)
	for _, name := range serviceNames {
		stableService, hasStable := stable[name]
		stableVariable := ""
		overrideVariable := "override" + name
		fmt.Fprintf(&generated, "\t%s := overrides[Beta%s]\n", overrideVariable, name)
		fmt.Fprintf(
			&generated,
			"\tif err := validateBeta%sOverride(%s); err != nil {\n\t\treturn err\n\t}\n",
			name,
			overrideVariable,
		)
		if hasStable {
			stableVariable = "stable" + name
			fmt.Fprintf(
				&generated,
				"\t%s, ok := stableServices[Beta%s].(v1.%sServer)\n",
				stableVariable,
				name,
				name,
			)
			fmt.Fprintf(&generated, "\tif !ok {\n")
			fmt.Fprintf(
				&generated,
				"\t\treturn fmt.Errorf(%q)\n",
				"stable implementation for "+name+" does not satisfy v1."+name+"Server",
			)
			fmt.Fprintf(&generated, "\t}\n")
		}

		fmt.Fprintf(&generated, "\tv1beta.Register%sServer(registrar, &beta%sAdapter{\n", name, name)
		if hasStable && len(stableService.methods) > 0 {
			fmt.Fprintf(&generated, "\t\tstable: %s,\n", stableVariable)
		}
		fmt.Fprintf(&generated, "\t\toverride: %s,\n", overrideVariable)
		fmt.Fprintf(&generated, "\t})\n")
	}
	generated.WriteString("\treturn nil\n}\n\n")

	for _, name := range serviceNames {
		betaService := beta[name]
		stableService, hasStable := stable[name]
		fmt.Fprintf(&generated, "type beta%sAdapter struct {\n", name)
		fmt.Fprintf(&generated, "\tv1beta.Unimplemented%sServer\n", name)
		if hasStable && len(stableService.methods) > 0 {
			fmt.Fprintf(&generated, "\tstable v1.%sServer\n", name)
		}
		generated.WriteString("\toverride any\n")
		generated.WriteString("}\n\n")
		fmt.Fprintf(
			&generated,
			"var _ v1beta.%sServer = (*beta%sAdapter)(nil)\n\n",
			name,
			name,
		)

		for _, betaMethod := range betaService.methods {
			stableMethod, shared := findMethod(stableService, betaMethod.name)
			if !hasStable {
				shared = false
			}
			writeAdapterMethod(&generated, betaService, betaMethod, stableMethod, shared)
		}
	}

	formatted, err := format.Source(generated.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated adapters: %w\n%s", err, generated.String())
	}
	return formatted, nil
}

func mapsKeys[V any](items map[string]V) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	return keys
}

func findMethod(service service, name string) (method, bool) {
	for _, candidate := range service.methods {
		if candidate.name == name {
			return candidate, true
		}
	}
	return method{}, false
}

func writeOverrideInterface(output *bytes.Buffer, service service, method method) {
	interfaceName := overrideInterfaceName(service, method)
	fmt.Fprintf(
		output,
		"// %s overrides the beta %s.%s method before stable adaptation.\n",
		interfaceName,
		service.name,
		method.name,
	)
	fmt.Fprintf(output, "type %s interface {\n", interfaceName)
	switch method.kind {
	case unaryMethod:
		fmt.Fprintf(
			output,
			"\t%s(context.Context, *v1beta.%s) (*v1beta.%s, error)\n",
			method.name,
			method.request,
			method.response,
		)
	case bidiStreamingMethod:
		fmt.Fprintf(
			output,
			"\t%s(grpc.BidiStreamingServer[v1beta.%s, v1beta.%s]) error\n",
			method.name,
			method.request,
			method.response,
		)
	}
	output.WriteString("}\n\n")
}

func writeOverrideValidator(output *bytes.Buffer, service service) {
	fmt.Fprintf(output, "func validateBeta%sOverride(override any) error {\n", service.name)
	output.WriteString("\tif override == nil {\n\t\treturn nil\n\t}\n")
	fmt.Fprintf(output, "\tif _, ok := override.(v1beta.%sServer); ok {\n", service.name)
	fmt.Fprintf(
		output,
		"\t\treturn fmt.Errorf(%q)\n",
		"beta override for "+service.name+
			" must implement focused method override interfaces, not v1beta."+service.name+"Server",
	)
	output.WriteString("\t}\n")
	for _, method := range service.methods {
		fmt.Fprintf(
			output,
			"\tif _, ok := override.(%s); ok {\n\t\treturn nil\n\t}\n",
			overrideInterfaceName(service, method),
		)
	}
	fmt.Fprintf(
		output,
		"\treturn fmt.Errorf(%q)\n",
		"beta override for "+service.name+" does not implement a generated focused method override interface",
	)
	output.WriteString("}\n\n")
}

func writeAdapterMethod(
	output *bytes.Buffer,
	service service,
	betaMethod method,
	stableMethod method,
	shared bool,
) {
	switch betaMethod.kind {
	case unaryMethod:
		writeUnaryAdapterMethod(output, service, betaMethod, stableMethod, shared)
	case bidiStreamingMethod:
		writeBidiAdapterMethod(output, service, betaMethod, stableMethod, shared)
	}
}

func writeUnaryAdapterMethod(
	output *bytes.Buffer,
	service service,
	betaMethod method,
	stableMethod method,
	shared bool,
) {
	fmt.Fprintf(
		output,
		`func (a *beta%sAdapter) %s(
	ctx context.Context,
	req *v1beta.%s,
) (*v1beta.%s, error) {
`,
		service.name,
		betaMethod.name,
		betaMethod.request,
		betaMethod.response,
	)
	fmt.Fprintf(
		output,
		"\tif override, ok := a.override.(%s); ok {\n\t\treturn override.%s(ctx, req)\n\t}\n",
		overrideInterfaceName(service, betaMethod),
		betaMethod.name,
	)
	if !shared {
		fmt.Fprintf(
			output,
			"\treturn a.Unimplemented%sServer.%s(ctx, req)\n}\n\n",
			service.name,
			betaMethod.name,
		)
		return
	}

	fmt.Fprintf(output, "\tstableRequest := new(v1.%s)\n", stableMethod.request)
	fmt.Fprintf(output, "\tif err := transcodeBetaRequest(req, stableRequest); err != nil {\n")
	fmt.Fprintf(
		output,
		"\t\treturn nil, fmt.Errorf(%q, err)\n",
		"convert "+service.name+"."+betaMethod.name+" request from beta to stable: %w",
	)
	output.WriteString("\t}\n")
	fmt.Fprintf(
		output,
		"\tstableResponse, err := a.stable.%s(ctx, stableRequest)\n",
		stableMethod.name,
	)
	output.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fmt.Fprintf(
		output,
		"\tif stableResponse == nil {\n\t\treturn nil, fmt.Errorf(%q)\n\t}\n",
		"stable "+service.name+"."+stableMethod.name+" returned a nil response",
	)
	fmt.Fprintf(output, "\tbetaResponse := new(v1beta.%s)\n", betaMethod.response)
	output.WriteString("\tif err := transcodeStableResponse(stableResponse, betaResponse); err != nil {\n")
	fmt.Fprintf(
		output,
		"\t\treturn nil, fmt.Errorf(%q, err)\n",
		"convert "+service.name+"."+betaMethod.name+" response from stable to beta: %w",
	)
	output.WriteString("\t}\n")
	output.WriteString("\treturn betaResponse, nil\n}\n\n")
}

func writeBidiAdapterMethod(
	output *bytes.Buffer,
	service service,
	betaMethod method,
	stableMethod method,
	shared bool,
) {
	fmt.Fprintf(
		output,
		"func (a *beta%sAdapter) %s(\n\tstream grpc.BidiStreamingServer[v1beta.%s, v1beta.%s],\n) error {\n",
		service.name,
		betaMethod.name,
		betaMethod.request,
		betaMethod.response,
	)
	fmt.Fprintf(
		output,
		"\tif override, ok := a.override.(%s); ok {\n\t\treturn override.%s(stream)\n\t}\n",
		overrideInterfaceName(service, betaMethod),
		betaMethod.name,
	)
	if !shared {
		fmt.Fprintf(
			output,
			"\treturn a.Unimplemented%sServer.%s(stream)\n}\n\n",
			service.name,
			betaMethod.name,
		)
		return
	}

	fmt.Fprintf(output, "\treturn a.stable.%s(&versionedBidiServerStream[\n", stableMethod.name)
	fmt.Fprintf(output, "\t\tv1.%s,\n", stableMethod.request)
	fmt.Fprintf(output, "\t\tv1.%s,\n", stableMethod.response)
	fmt.Fprintf(output, "\t\tv1beta.%s,\n", betaMethod.request)
	fmt.Fprintf(output, "\t\tv1beta.%s,\n", betaMethod.response)
	output.WriteString("\t]{\n")
	output.WriteString("\t\tServerStream: stream,\n")
	output.WriteString("\t\tbeta:         stream,\n")
	fmt.Fprintf(
		output,
		"\t\toperation:    %q,\n",
		"azd.extensions.v1beta."+service.name+"/"+betaMethod.name,
	)
	fmt.Fprintf(
		output,
		"\t\trequestToStable: func(request *v1beta.%s) (*v1.%s, error) {\n",
		betaMethod.request,
		stableMethod.request,
	)
	fmt.Fprintf(output, "\t\t\tstableRequest := new(v1.%s)\n", stableMethod.request)
	output.WriteString("\t\t\tif err := transcodeBetaRequest(request, stableRequest); err != nil {\n")
	output.WriteString("\t\t\t\treturn nil, err\n\t\t\t}\n")
	output.WriteString("\t\t\treturn stableRequest, nil\n\t\t},\n")
	fmt.Fprintf(
		output,
		"\t\tresponseToBeta: func(response *v1.%s) (*v1beta.%s, error) {\n",
		stableMethod.response,
		betaMethod.response,
	)
	fmt.Fprintf(output, "\t\t\tbetaResponse := new(v1beta.%s)\n", betaMethod.response)
	output.WriteString("\t\t\tif err := transcodeStableResponse(response, betaResponse); err != nil {\n")
	output.WriteString("\t\t\t\treturn nil, err\n\t\t\t}\n")
	output.WriteString("\t\t\treturn betaResponse, nil\n\t\t},\n")
	output.WriteString("\t})\n}\n\n")
}

func overrideInterfaceName(service service, method method) string {
	return "Beta" + service.name + method.name + "Override"
}
