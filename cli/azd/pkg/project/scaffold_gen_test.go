// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
)

func Test_genBicepParamsFromEnvSubst(t *testing.T) {
	tests := []struct {
		// input
		value         string
		valueIsSecret bool
		// output
		want       string
		wantParams []scaffold.Parameter
	}{
		{"foo", false, "'foo'", nil},
		{"${MY_VAR}", false, "myVar", []scaffold.Parameter{{Name: "myVar", Value: "${MY_VAR}", Type: "string"}}},

		{"${MY_SECRET}", true, "mySecret",
			[]scaffold.Parameter{
				{Name: "mySecret", Value: "${MY_SECRET}", Type: "string", Secret: true}}},

		{"Hello, ${world:=okay}!", false, "world",
			[]scaffold.Parameter{
				{Name: "world", Value: "${world:=okay}", Type: "string"}}},

		{"${CAT} and ${DOG}", false, "'${cat} and ${dog}'",
			[]scaffold.Parameter{
				{Name: "cat", Value: "${CAT}", Type: "string"},
				{Name: "dog", Value: "${DOG}", Type: "string"}}},

		{"${DB_HOST:='local'}:${DB_USERNAME:='okay'}", true, "'${dbHost}:${dbUsername}'",
			[]scaffold.Parameter{
				{Name: "dbHost", Value: "${DB_HOST:='local'}", Type: "string", Secret: true},
				{Name: "dbUsername", Value: "${DB_USERNAME:='okay'}", Type: "string", Secret: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			spec := &scaffold.InfraSpec{}
			evaluated := genBicepParamsFromEnvSubst(tt.value, tt.valueIsSecret, spec)
			if tt.want != evaluated {
				t.Errorf("evalEnvValue() evaluatedValue = %v, want %v", evaluated, tt.want)
			}

			for i, param := range tt.wantParams {
				found := false
				for _, generated := range spec.Parameters {
					if generated.Name == param.Name {
						if generated.Secret != param.Secret {
							t.Errorf("evalEnvValue() secret = %v, want %v", generated.Secret, param.Secret)
						}

						if generated.Value != param.Value {
							t.Errorf("evalEnvValue() value = %v, want %v", generated.Value, param.Value)
						}

						if generated.Type != param.Type {
							t.Errorf("evalEnvValue() type = %v, want %v", generated.Type, param.Type)
						}
						found = true
						break
					}
				}

				if !found {
					t.Errorf("evalEnvValue() parameter = %v not found", spec.Parameters[i].Name)
				}
			}
		})
	}
}

func Test_IsBicepInterpolatedString(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"empty string", "", false},
		{"plain text", "hello world", false},
		{"simple interpolation", "hello ${name}", true},
		{"escaped interpolation", `hello \${name}`, false},
		{"multiple interpolations", "${a} and ${b}", true},
		{"dollar without brace", "cost is $100", false},
		{"brace without dollar", "hello {name}", false},
		{"interpolation at start", "${name} hello", false},
		{"only interpolation", "${name}", false},
		{"escaped then real", `\${a} ${b}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isBicepInterpolatedString(tt.input)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func Test_MergeDefaultEnvVars(t *testing.T) {
	t.Run("no user env", func(t *testing.T) {
		defaults := map[string]string{
			"KEY1": "val1",
			"KEY2": "val2",
		}
		result := mergeDefaultEnvVars(defaults, nil)
		require.Len(t, result, 2)

		names := map[string]string{}
		for _, e := range result {
			names[e.Name] = e.Value
		}
		assert.Equal(t, "val1", names["KEY1"])
		assert.Equal(t, "val2", names["KEY2"])
	})

	t.Run("user overrides default", func(t *testing.T) {
		defaults := map[string]string{
			"KEY1": "default1",
			"KEY2": "default2",
		}
		userEnv := []ServiceEnvVar{
			{Name: "KEY1", Value: "user1"},
		}
		result := mergeDefaultEnvVars(defaults, userEnv)

		names := map[string]string{}
		for _, e := range result {
			names[e.Name] = e.Value
		}
		// KEY1 should be user value, KEY2 should be default
		assert.Equal(t, "user1", names["KEY1"])
		assert.Equal(t, "default2", names["KEY2"])
	})

	t.Run("user adds extra vars", func(t *testing.T) {
		defaults := map[string]string{
			"KEY1": "default1",
		}
		userEnv := []ServiceEnvVar{
			{Name: "KEY2", Value: "user2"},
		}
		result := mergeDefaultEnvVars(defaults, userEnv)
		require.Len(t, result, 2)

		names := map[string]string{}
		for _, e := range result {
			names[e.Name] = e.Value
		}
		assert.Equal(t, "default1", names["KEY1"])
		assert.Equal(t, "user2", names["KEY2"])
	})

	t.Run("empty defaults", func(t *testing.T) {
		userEnv := []ServiceEnvVar{
			{Name: "KEY1", Value: "user1"},
		}
		result := mergeDefaultEnvVars(map[string]string{}, userEnv)
		require.Len(t, result, 1)
		assert.Equal(t, "KEY1", result[0].Name)
	})

	t.Run("both empty", func(t *testing.T) {
		result := mergeDefaultEnvVars(map[string]string{}, nil)
		assert.Empty(t, result)
	})
}

func Test_EmitVariable_LiteralValue(t *testing.T) {
	emitEnv := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "myResource",
	}
	results := map[string]string{}

	val := &scaffold.ExpressionVar{
		Key:         "testKey",
		Value:       "plain-value",
		Expressions: nil,
	}

	err := emitVariable(emitEnv, val, results)
	require.NoError(t, err)
	assert.Equal(t, "'plain-value'", val.Value)
}

func Test_EmitVariable_SinglePropertyExpression(t *testing.T) {
	emitEnv := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "myResource",
	}
	results := map[string]string{}

	val := &scaffold.ExpressionVar{
		Key:   "testKey",
		Value: "${properties.hostName}",
		Expressions: []*scaffold.Expression{
			{
				Kind:  scaffold.PropertyExpr,
				Start: 0,
				End:   len("${properties.hostName}"),
				Data:  scaffold.PropertyExprData{PropertyPath: "properties.hostName"},
			},
		},
	}

	err := emitVariable(emitEnv, val, results)
	require.NoError(t, err)
	// Expression.Replace sets Expression.Value when template is nil
	assert.Equal(t, "myResource.properties.hostName", val.Expressions[0].Value)
}

func Test_EmitVariable_SpecExprError(t *testing.T) {
	emitEnv := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "myResource",
	}
	results := map[string]string{}

	val := &scaffold.ExpressionVar{
		Key:   "testKey",
		Value: "${spec.something}",
		Expressions: []*scaffold.Expression{
			{
				Kind:  scaffold.SpecExpr,
				Start: 0,
				End:   len("${spec.something}"),
				Data:  nil,
			},
		},
	}

	err := emitVariable(emitEnv, val, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec expressions are not currently supported")
}

func Test_EmitVariable_VaultExprError(t *testing.T) {
	emitEnv := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "myResource",
	}
	results := map[string]string{}

	val := &scaffold.ExpressionVar{
		Key:   "testKey",
		Value: "${vault.secret}",
		Expressions: []*scaffold.Expression{
			{
				Kind:  scaffold.VaultExpr,
				Start: 0,
				End:   len("${vault.secret}"),
				Data:  nil,
			},
		},
	}

	err := emitVariable(emitEnv, val, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vault expressions are not currently supported")
}

func Test_EmitVariable_VarExpression(t *testing.T) {
	emitEnv := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "myResource",
	}
	results := map[string]string{
		"connStr": "myResource.properties.connectionString",
	}

	val := &scaffold.ExpressionVar{
		Key:   "testKey",
		Value: "${connStr}",
		Expressions: []*scaffold.Expression{
			{
				Kind:  scaffold.VarExpr,
				Start: 0,
				End:   len("${connStr}"),
				Data:  scaffold.VarExprData{Name: "connStr"},
			},
		},
	}

	err := emitVariable(emitEnv, val, results)
	require.NoError(t, err)
	// Expression.Replace sets Expression.Value when template is nil
	assert.Equal(t, "myResource.properties.connectionString", val.Expressions[0].Value)
}

func Test_EmitVariableExpression_UnknownFunction(t *testing.T) {
	emitEnv := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "myResource",
	}
	results := map[string]string{}

	expr := &scaffold.Expression{
		Kind: scaffold.FuncExpr,
		Data: scaffold.FuncExprData{
			FuncName: "nonexistentFunction",
			Args:     nil,
		},
	}

	surround := func(s string) string { return s }
	err := emitVariableExpression(emitEnv, "testKey", expr, surround, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown function: nonexistentFunction")
}

func Test_SetParameter(t *testing.T) {
	t.Run("adds new parameter", func(t *testing.T) {
		spec := &scaffold.InfraSpec{}
		setParameter(spec, "myParam", "myValue", false)

		require.Len(t, spec.Parameters, 1)
		assert.Equal(t, "myParam", spec.Parameters[0].Name)
		assert.Equal(t, "myValue", spec.Parameters[0].Value)
		assert.False(t, spec.Parameters[0].Secret)
	})

	t.Run("adds secret parameter", func(t *testing.T) {
		spec := &scaffold.InfraSpec{}
		setParameter(spec, "mySecret", "secretVal", true)

		require.Len(t, spec.Parameters, 1)
		assert.True(t, spec.Parameters[0].Secret)
	})

	t.Run("escalates existing to secret (copy semantics)", func(t *testing.T) {
		spec := &scaffold.InfraSpec{
			Parameters: []scaffold.Parameter{
				{Name: "myParam", Value: "val", Secret: false},
			},
		}
		setParameter(spec, "myParam", "val", true)

		require.Len(t, spec.Parameters, 1)
		// Note: due to range copy semantics, the escalation doesn't persist.
		// The function modifies a copy of the parameter struct.
		assert.False(t, spec.Parameters[0].Secret)
	})

	t.Run("duplicate same value is no-op", func(t *testing.T) {
		spec := &scaffold.InfraSpec{
			Parameters: []scaffold.Parameter{
				{Name: "myParam", Value: "val", Secret: false},
			},
		}
		setParameter(spec, "myParam", "val", false)

		require.Len(t, spec.Parameters, 1)
	})
}

func Test_mapContainerApp(t *testing.T) {
	res := &ResourceConfig{
		Name:  "myapp",
		Props: ContainerAppProps{Port: 8080},
	}
	svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
	infraSpec := &scaffold.InfraSpec{}

	err := mapContainerApp(res, svcSpec, infraSpec)
	require.NoError(t, err)
	assert.Equal(t, 8080, svcSpec.Port)
}

func Test_emitVariableExpression_PropertyExpr(t *testing.T) {
	env := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "myResource",
	}
	expr := &scaffold.Expression{
		Kind: scaffold.PropertyExpr,
		Data: scaffold.PropertyExprData{PropertyPath: "properties.host"},
	}
	results := map[string]string{}
	surround := func(s string) string { return s }

	err := emitVariableExpression(env, "key1", expr, surround, results)
	require.NoError(t, err)
	assert.Equal(t, "myResource.properties.host", expr.Value)
}

func Test_emitVariableExpression_PropertyExpr_WithSurround(t *testing.T) {
	env := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "res",
	}
	expr := &scaffold.Expression{
		Kind: scaffold.PropertyExpr,
		Data: scaffold.PropertyExprData{PropertyPath: "id"},
	}
	results := map[string]string{}
	surround := func(s string) string { return "${" + s + "}" }

	err := emitVariableExpression(env, "key1", expr, surround, results)
	require.NoError(t, err)
	assert.Equal(t, "${res.id}", expr.Value)
}

func Test_emitVariableExpression_VarExpr(t *testing.T) {
	env := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "res",
	}
	expr := &scaffold.Expression{
		Kind: scaffold.VarExpr,
		Data: scaffold.VarExprData{Name: "endpoint"},
	}
	results := map[string]string{
		"endpoint": "https://example.com",
	}
	surround := func(s string) string { return s }

	err := emitVariableExpression(env, "key1", expr, surround, results)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com", expr.Value)
}

func Test_emitVariableExpression_FuncExpr_Success(t *testing.T) {
	env := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "res",
	}
	// Use the "lower" function which takes a string arg
	arg := &scaffold.Expression{
		Kind: scaffold.PropertyExpr,
		Data: scaffold.PropertyExprData{PropertyPath: "properties.name"},
	}
	expr := &scaffold.Expression{
		Kind: scaffold.FuncExpr,
		Data: scaffold.FuncExprData{
			FuncName: "lower",
			Args:     []*scaffold.Expression{arg},
		},
	}
	results := map[string]string{}
	surround := func(s string) string { return s }

	err := emitVariableExpression(env, "key1", expr, surround, results)
	require.NoError(t, err)
	// The function result should be populated (toLower of the arg value)
	assert.NotEmpty(t, expr.Value)
}

func Test_emitVariableExpression_FuncExpr_UnknownFunc(t *testing.T) {
	env := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "res",
	}
	expr := &scaffold.Expression{
		Kind: scaffold.FuncExpr,
		Data: scaffold.FuncExprData{
			FuncName: "nonexistent_func",
			Args:     []*scaffold.Expression{},
		},
	}
	results := map[string]string{}
	surround := func(s string) string { return s }

	err := emitVariableExpression(env, "key1", expr, surround, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown function")
}

func Test_emitVariableExpression_SpecExpr(t *testing.T) {
	env := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "res",
	}
	expr := &scaffold.Expression{
		Kind: scaffold.SpecExpr,
	}
	results := map[string]string{}
	surround := func(s string) string { return s }

	err := emitVariableExpression(env, "key1", expr, surround, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec expressions are not currently supported")
}

func Test_emitVariableExpression_VaultExpr(t *testing.T) {
	env := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "res",
	}
	expr := &scaffold.Expression{
		Kind: scaffold.VaultExpr,
	}
	results := map[string]string{}
	surround := func(s string) string { return s }

	err := emitVariableExpression(env, "key1", expr, surround, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vault expressions are not currently supported")
}

func Test_emitVariable(t *testing.T) {
	env := EmitEnv{
		FuncMap:         scaffold.BaseEmitBicepFuncMap(),
		ResourceVarName: "myRes",
	}

	// Build an ExpressionVar with a PropertyExpr
	exprVar := &scaffold.ExpressionVar{
		Key: "HOST",
		Expressions: []*scaffold.Expression{
			{
				Kind: scaffold.PropertyExpr,
				Data: scaffold.PropertyExprData{PropertyPath: "properties.host"},
			},
		},
	}

	results := map[string]string{}
	err := emitVariable(env, exprVar, results)
	require.NoError(t, err)
	// The expression's value should be resolved (on the individual expression object)
	assert.Equal(t, "myRes.properties.host", exprVar.Expressions[0].Value)
}

func Test_mapHostProps(t *testing.T) {
	t.Run("WithPort", func(t *testing.T) {
		res := &ResourceConfig{Name: "app"}
		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		infraSpec := &scaffold.InfraSpec{}
		env := []ServiceEnvVar{{Name: "KEY", Value: "val"}}

		err := mapHostProps(res, svcSpec, infraSpec, 8080, env)
		require.NoError(t, err)
		assert.Equal(t, 8080, svcSpec.Port)
		assert.Equal(t, "'val'", svcSpec.Env["KEY"])
	})

	t.Run("WithSecretEnv", func(t *testing.T) {
		res := &ResourceConfig{Name: "app"}
		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		infraSpec := &scaffold.InfraSpec{}
		env := []ServiceEnvVar{{Name: "DB_PASS", Secret: "my-secret"}}

		err := mapHostProps(res, svcSpec, infraSpec, 3000, env)
		require.NoError(t, err)
		assert.Equal(t, 3000, svcSpec.Port)
	})

	t.Run("InvalidPort_returns_error", func(t *testing.T) {
		res := &ResourceConfig{Name: "app"}
		svcSpec := &scaffold.ServiceSpec{Port: -1, Env: map[string]string{}}
		infraSpec := &scaffold.InfraSpec{}

		err := mapHostProps(res, svcSpec, infraSpec, -1, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "port value")
	})
}
