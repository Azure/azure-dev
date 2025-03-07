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

func BaseFuncMap() FuncMap {
	return FuncMap{
		"lower":                     strings.ToLower,
		"upper":                     strings.ToUpper,
		"replace":                   strings.ReplaceAll,
		"host":                      hostFromEndpoint,
		"aiProjectConnectionString": aiProjectConnectionString,
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

	defaultFuncMap := BaseFuncMap()
	if env.FuncMap == nil {
		env.FuncMap = make(FuncMap, len(defaultFuncMap))
	}
	maps.Copy(env.FuncMap, defaultFuncMap)

	// parse into map of expression values
	evalValues := make([]*expressionVar, 0, len(values))
	results := make(map[string]*expressionVar, len(values))

	for key, value := range values {
		exp := &expressionVar{
			key:   key,
			value: value,
		}

		expressions, err := Parse(&exp.value)
		if err != nil {
			return nil, fmt.Errorf("failed to parse expression '%s': %w", exp.value, err)
		}

		if len(expressions) == 0 {
			results[key] = exp
			continue
		}

		exp.expressions = expressions

		// parse dependencies
		for _, expr := range exp.expressions {
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
	slices.SortFunc(evalValues, func(a, b *expressionVar) int {
		return strings.Compare(a.key, b.key)
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

		for _, expr := range val.expressions {
			err := evalExpression(env, val.key, &expr, results)
			if err != nil {
				return nil, fmt.Errorf("evaluating key '%s': %w", val.key, err)
			}

			results[val.key] = val
		}
	}

	// return resolved values as map
	for key, val := range results {
		values[key] = val.value
	}

	return values, nil
}

func evalExpression(env EvalEnv, key string, expr *Expression, results map[string]*expressionVar) error {
	switch expr.Kind {
	case VarExpr:
		// Variable reference
		varName := expr.Data.(VarExprData).Name
		expr.Replace(results[varName].value)
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
		// Vault expression ${vault.xxx} or ${vault.}
		secretPath := expr.Data.(VaultExprData).SecretPath
		if secretPath == "" {
			// the canonical secret path is the key, but we need to replace _ with -
			// to match the vault secret name
			secretPath = strings.ReplaceAll(key, "_", "-")
		}

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
		funcResult, err := callFn(fn, funcName, args)
		if err != nil {
			return fmt.Errorf("calling '%s' failed: %w", funcName, err)
		}

		resultString := fmt.Sprintf("%v", funcResult)
		expr.Replace(resultString)
	}

	return nil
}

// expressionVar represents an expression variable to be resolved, and its final output value.
type expressionVar struct {
	// The name of the variable
	key string

	// The value of the expression
	//
	// When initially created, this is the raw value of the expression.
	// When the expression is resolved, this is the resolved value.
	value string

	// The expressions parsed from the value. Can be nil if the value does not contain any expressions.
	expressions []Expression

	// Variables that this variable depends on.
	dependsOn []string
}

func nextVal(evalCtx []*expressionVar, results map[string]*expressionVar) (*expressionVar, error) {
	allDone := true

	for _, val := range evalCtx {
		if results[val.key] != nil {
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

// callFn handles dynamic function calling with proper type reflection
func callFn(fn interface{}, funcName string, args []interface{}) (interface{}, error) {
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
