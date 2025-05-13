// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"maps"

	"github.com/azure/azure-dev/cli/azd/pkg/yamlnode"
	"github.com/braydonk/yaml"
	"github.com/tidwall/gjson"
)

type EvalEnv struct {
	// ResourceSpec is the azure.yaml resource spec.
	ResourceSpec *yaml.Node

	// ArmResource is the Azure resource representation.
	ArmResource string

	// VaultSecret is a function that resolves a secret from the vault.
	VaultSecret func(string) (string, error)

	// FuncMap is a map of function names to their implementations.
	FuncMap FuncMap
}

// FuncMap is the type of the map defining the mapping from names to functions.
// Each function must have either a single return value, or two return values of which the second has type error.
// In that case, if the second (error) return value evaluates to non-nil during execution,
// execution terminates and Eval returns that error.
type FuncMap map[string]any

// BaseEvalFuncMap returns a map of functions that can be used for evaluation purposes.
//
// The functions are evaluated at runtime against live Azure resources.
func BaseEvalFuncMap() FuncMap {
	return FuncMap{
		"lower":             strings.ToLower,
		"upper":             strings.ToUpper,
		"replace":           strings.ReplaceAll,
		"host":              hostFromEndpoint,
		"aiProjectEndpoint": aiProjectEndpoint,
	}
}

// BaseEmitFuncMap returns a map of functions that can be used for bicep emitting purposes.
//
// The functions are similar to the base function map, except they are compile-time expressions that operate
// on the string symbols of the variables, rather than their resolved values.
// The functions are not evaluated at runtime, but rather emitted as part of the Bicep template.
func BaseEmitBicepFuncMap() FuncMap {
	return FuncMap{
		"lower":             bicepFuncCall("toLower"),
		"upper":             bicepFuncCall("toUpper"),
		"replace":           bicepFuncCallThree("replace"),
		"host":              emitHostFromEndpoint,
		"aiProjectEndpoint": emitAiProjectEndpoint,
	}
}

// Eval evaluates the given values using the provided environment.
// It replaces the expressions in the values with their resolved values.
// It returns the resolved values as a map.
//
// The function supports the following types of expressions:
// - Variable references, ${varName}
// - ARM Property expressions, ${.property.path}
// - Spec expressions, ${spec.property.path}
// - Literal expressions, ${"literal"} (for use in function expressions)
// - Vault expressions, ${vault.secretName} OR ${vault.} -- the latter will use the key as the secret name
// - Function expressions (only a single-level of nesting), ${replace "management.azure.com/" spec.id}
//
// The function also supports nested expressions and circular dependencies.
func Eval(values map[string]string, env EvalEnv) (map[string]string, error) {
	if env.ArmResource == "" {
		return values, fmt.Errorf("missing arm resource")
	}

	if env.ResourceSpec == nil {
		return values, fmt.Errorf("missing resource spec")
	}

	if env.VaultSecret == nil {
		return values, fmt.Errorf("missing vault secret resolver")
	}

	defaultFuncMap := BaseEvalFuncMap()
	if env.FuncMap == nil {
		env.FuncMap = make(FuncMap, len(defaultFuncMap))
	}
	maps.Copy(env.FuncMap, defaultFuncMap)

	evaluator := func(value *ExpressionVar, results map[string]string) error {
		return evalVariable(env, value, results)
	}

	return resolveVariables(values, evaluator)
}

// EmitBicep emits the given variables suitable for Bicep.
func EmitBicep(values map[string]string, emitter Resolver) (map[string]string, error) {
	return resolveVariables(values, emitter)
}

// Resolver is a function that resolves a variable expression.
// It takes the expression value, and the current results map.
// The function should evaluate expressions within value and call Replace.
type Resolver func(value *ExpressionVar, results map[string]string) error

func resolveVariables(values map[string]string, resolver Resolver) (map[string]string, error) {
	// parse into map of expression values
	evalValues := make([]*ExpressionVar, 0, len(values))
	results := make(map[string]string, len(values))

	for key, value := range values {
		exp := &ExpressionVar{
			Key:   key,
			Value: value,
		}

		expressions, err := Parse(&exp.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to parse expression '%s': %w", exp.Value, err)
		}

		exp.Expressions = expressions

		// parse dependencies
		for _, expr := range exp.Expressions {
			switch expr.Kind {
			case VarExpr:
				dependOn := expr.Data.(VarExprData).Name

				if _, ok := values[dependOn]; !ok {
					return nil, fmt.Errorf("missing dependency: %s", dependOn)
				}

				exp.dependsOn = append(exp.dependsOn, dependOn)
			case FuncExpr:
				funcData := expr.Data.(FuncExprData)
				for _, arg := range funcData.Args {
					if arg.Kind == VarExpr {
						dependOn := arg.Data.(VarExprData).Name
						if _, ok := values[dependOn]; !ok {
							return nil, fmt.Errorf("missing dependency for function argument: %s", dependOn)
						}

						exp.dependsOn = append(exp.dependsOn, dependOn)
					}
				}
			}
		}

		evalValues = append(evalValues, exp)
	}

	// sort for determinism
	slices.SortFunc(evalValues, func(a, b *ExpressionVar) int {
		return strings.Compare(a.Key, b.Key)
	})

	// evaluate expressions
	for {
		val, err := nextVal(evalValues, results)
		if err != nil {
			return nil, fmt.Errorf("evaluating values: %w", err)
		}

		if val == nil {
			// done
			break
		}

		err = resolver(val, results)
		if err != nil {
			return nil, err
		}

		val.done = true
		results[val.Key] = val.Value
	}

	return results, nil
}

func evalVariable(env EvalEnv, val *ExpressionVar, results map[string]string) error {
	for _, expr := range val.Expressions {
		err := evalExpression(env, val.Key, expr, results)
		if err != nil {
			return fmt.Errorf("evaluating key '%s': %w", val.Key, err)
		}
	}

	return nil
}

func evalExpression(env EvalEnv, key string, expr *Expression, results map[string]string) error {
	switch expr.Kind {
	case VarExpr:
		// Variable reference
		varName := expr.Data.(VarExprData).Name
		expr.Replace(results[varName])
	case PropertyExpr:
		// Property expression ${.xxx}
		path := expr.Data.(PropertyExprData).PropertyPath
		result := gjson.Get(env.ArmResource, path)
		if !result.Exists() {
			return fmt.Errorf("arm property '%s' does not exist", path)
		}

		expr.Replace(result.String())
	case SpecExpr:
		// Spec expression ${spec.xxx}
		path := expr.Data.(SpecExprData).PropertyPath
		node, err := yamlnode.Find(env.ResourceSpec, path)
		if err != nil {
			return fmt.Errorf("spec property '%s' does not exist: %w", path, err)
		}

		expr.Replace(node.Value)
	case LiteralExpr:
		// Literal expression ${"xxx"}
		literal := expr.Data.(LiteralExprData).Value
		expr.Replace(literal)
	case VaultExpr:
		// Vault expression ${vault.xxx}
		secretPath := expr.Data.(VaultExprData).SecretPath
		secret, err := env.VaultSecret(secretPath)
		if err != nil {
			return fmt.Errorf("failed to get secret '%s': %w", secretPath, err)
		}

		expr.Replace(secret)
	case FuncExpr:
		funcData := expr.Data.(FuncExprData)
		funcName := funcData.FuncName

		// Check if function exists
		fn, ok := env.FuncMap[funcName]
		if !ok {
			return fmt.Errorf("unknown function: %s", funcName)
		}

		// Evaluate all arguments
		args := make([]interface{}, 0, len(funcData.Args))
		for _, arg := range funcData.Args {
			err := evalExpression(env, key, arg, results)
			if err != nil {
				return fmt.Errorf("evaluating arguments for '%s': %w", funcName, err)
			}

			args = append(args, arg.Value)
		}

		// Call the function
		funcResult, err := CallFn(fn, funcName, args)
		if err != nil {
			return fmt.Errorf("calling '%s' failed: %w", funcName, err)
		}

		resultString := fmt.Sprintf("%v", funcResult)
		expr.Replace(resultString)
	}

	return nil
}

// ExpressionVar represents an expression variable to be resolved, and its final output value.
type ExpressionVar struct {
	// The name of the variable
	Key string

	// The Value of the expression
	//
	// When initially created, this is the raw Value of the expression.
	// When the expression is resolved, this is the resolved Value.
	Value string

	// The Expressions parsed from the value. Can be nil if the value does not contain any Expressions.
	Expressions []*Expression

	// done indicates whether the expression has been resolved.
	done bool

	// Variables that this variable depends on.
	dependsOn []string
}

func nextVal(evalCtx []*ExpressionVar, results map[string]string) (*ExpressionVar, error) {
	allDone := true

	for _, val := range evalCtx {
		if val.done {
			// already done
			continue
		}

		allDone = false
		allDependenciesMet := true
		for _, key := range val.dependsOn {
			if _, ok := results[key]; !ok {
				allDependenciesMet = false
				break
			}
		}

		if allDependenciesMet {
			return val, nil
		}
	}

	if !allDone {
		return nil, errors.New("circular dependencies detected")
	}

	return nil, nil
}

// CallFn handles dynamic function calling with proper type reflection
func CallFn(fn interface{}, funcName string, args []interface{}) (interface{}, error) {
	// Use reflection to call the function with proper types
	funcValue := reflect.ValueOf(fn)
	funcType := funcValue.Type()

	// Validate function signature according to FuncMap requirements
	numOut := funcType.NumOut()
	if numOut != 1 && numOut != 2 {
		return nil, fmt.Errorf("function '%s' must have one or two return values", funcName)
	}
	if numOut == 2 && !funcType.Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		return nil, fmt.Errorf("second return value of function '%s' must be error", funcName)
	}

	// Create properly typed arguments
	reflectArgs := make([]reflect.Value, len(args))
	for i, arg := range args {
		// Convert each argument to the type expected by the function
		if i < funcType.NumIn() {
			expectedType := funcType.In(i)
			argValue := reflect.ValueOf(arg)

			// If types don't match but can be converted
			if argValue.Type().ConvertibleTo(expectedType) {
				reflectArgs[i] = argValue.Convert(expectedType)
			} else {
				reflectArgs[i] = argValue
			}
		} else {
			reflectArgs[i] = reflect.ValueOf(arg)
		}
	}

	// Call the function with the properly typed arguments
	resultValues := funcValue.Call(reflectArgs)

	// Process the results according to FuncMap requirements
	var funcResult interface{}

	// First return value is the result
	if len(resultValues) > 0 {
		funcResult = resultValues[0].Interface()
	}

	// Check for error if there's a second return value
	if len(resultValues) == 2 && !resultValues[1].IsNil() {
		err := resultValues[1].Interface().(error)
		return nil, fmt.Errorf("function '%s' returned error: %w", funcName, err)
	}

	return funcResult, nil
}
