package scaffold

import (
	"errors"
	"fmt"
	"log"
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
	FuncMap map[string]FunctionCall
}

type FunctionCall func([]string) (string, error)

func BaseFuncMap() map[string]FunctionCall {
	return map[string]FunctionCall{
		// Add basic string manipulation functions
		"concat": func(args []string) (string, error) {
			return strings.Join(args, ""), nil
		},
		"lower": func(args []string) (string, error) {
			if len(args) != 1 {
				return "", fmt.Errorf("lower function requires exactly 1 argument")
			}
			return strings.ToLower(args[0]), nil
		},
		"upper": func(args []string) (string, error) {
			if len(args) != 1 {
				return "", fmt.Errorf("upper function requires exactly 1 argument")
			}
			return strings.ToUpper(args[0]), nil
		},
		"replace": func(args []string) (string, error) {
			if len(args) != 3 {
				return "", fmt.Errorf("replace function requires exactly 3 arguments: string, old, new")
			}
			return strings.Replace(args[0], args[1], args[2], -1), nil
		},
		"host": func(args []string) (string, error) {
			if len(args) != 1 {
				return "", fmt.Errorf("host function requires exactly 1 argument")
			}
			endpoint := args[0]
			// Extract the host from the endpoint
			host := strings.Split(strings.ReplaceAll(endpoint, "/", ""), ":")[1]
			return host, nil
		},
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
// - Function expressions (only a single-level of nesting), ${concat "management.azure.com/" spec.id}
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
		env.FuncMap = make(map[string]FunctionCall, len(defaultFuncMap))
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
		args := make([]string, 0, len(funcData.Args))
		for _, arg := range funcData.Args {
			err := evalExpression(env, key, arg, results)
			if err != nil {
				return fmt.Errorf("evaluating arguments for '%s': %w", funcName, err)
			}

			args = append(args, arg.Value)
		}

		log.Println("evaluating function", funcName, "with args", args)
		// Call the function
		funcResult, err := fn(args)
		if err != nil {
			return fmt.Errorf("calling '%s' failed: %w", funcName, err)
		}

		expr.Replace(funcResult)
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
