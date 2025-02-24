package scaffold

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/yamlnode"
	"github.com/braydonk/yaml"
	"github.com/tidwall/gjson"
)

type EvalCtx struct {
	// ResourceSpec is the azure.yaml resource spec.
	ResourceSpec *yaml.Node

	// ArmResource is the Azure resource representation.
	ArmResource string

	// VaultSecret is a function that resolves a secret from the vault.
	VaultSecret func(string) (string, error)
}

func Eval(values map[string]string, context EvalCtx) (map[string]string, error) {
	if context.ArmResource == "" {
		return values, fmt.Errorf("missing arm resource")
	}

	if context.ResourceSpec == nil {
		return values, fmt.Errorf("missing resource spec")
	}

	if context.VaultSecret == nil {
		return values, fmt.Errorf("missing vault secret resolver")
	}

	// parse into map of expression values
	evalValues := make([]*expressionVar, 0, len(values))
	results := make(map[string]*expressionVar, len(values))

	for key, value := range values {
		exp := &expressionVar{
			key:   key,
			value: value,
		}

		expressions, err := parseExpressions(&exp.value)
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
			switch expr.Kind {
			case VarExpr:
				envVarName := expr.Data.(VarExprData).Name
				expr.Replace(results[envVarName].value)
			case SpecExpr:
				path := expr.Data.(SpecExprData).PropertyPath
				node, _ := yamlnode.Find(context.ResourceSpec, path)
				if node != nil {
					expr.Replace(node.Value)
				}
			case PropertyExpr:
				path := expr.Data.(PropertyExprData).PropertyPath
				result := gjson.Get(context.ArmResource, path)
				if result.Exists() {
					expr.Replace(result.String())
				}
			case VaultExpr:
				secretPath := expr.Data.(VaultExprData).SecretPath
				if secretPath == "" {
					// the canonical secret path is the key, but we need to replace _ with -
					// to match the vault secret name
					secretPath = strings.ReplaceAll(val.key, "_", "-")
				}

				secret, err := context.VaultSecret(secretPath)
				if err != nil {
					return nil, fmt.Errorf("failed to get secret '%s': %w", secretPath, err)
				}
				expr.Replace(secret)
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
	expressions []expression

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
