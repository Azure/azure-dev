// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- emitVariableExpression: cover all expression kinds ----

func Test_emitVariableExpression_PropertyExpr_Coverage3(t *testing.T) {
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

func Test_emitVariableExpression_PropertyExpr_WithSurround_Coverage3(t *testing.T) {
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

func Test_emitVariableExpression_VarExpr_Coverage3(t *testing.T) {
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

func Test_emitVariableExpression_FuncExpr_Success_Coverage3(t *testing.T) {
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

func Test_emitVariableExpression_FuncExpr_UnknownFunc_Coverage3(t *testing.T) {
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

func Test_emitVariableExpression_SpecExpr_Coverage3(t *testing.T) {
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

func Test_emitVariableExpression_VaultExpr_Coverage3(t *testing.T) {
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

// ---- emitVariable via emitVariableExpression path ----

func Test_emitVariable_Coverage3(t *testing.T) {
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

// ---- ArtifactCollection.ToString ----

func Test_ArtifactCollectionToString_Coverage3(t *testing.T) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindEndpoint,
			Location:     "https://app.azurewebsites.net",
			LocationKind: LocationKindRemote,
		},
		{
			Kind:         ArtifactKindContainer,
			Location:     "myacr.azurecr.io/app:latest",
			LocationKind: LocationKindRemote,
		},
		{
			Kind:         ArtifactKindArchive,
			Location:     "/tmp/deploy.zip",
			LocationKind: LocationKindLocal,
		},
	}

	result := ac.ToString("")
	assert.Contains(t, result, "https://app.azurewebsites.net")
	assert.Contains(t, result, "Remote Image")
	assert.Contains(t, result, "Package Output")
}

func Test_ArtifactCollectionToString_Empty_Coverage3(t *testing.T) {
	ac := ArtifactCollection{}
	result := ac.ToString("")
	assert.Contains(t, result, "No artifacts")
}

func Test_ArtifactCollectionToString_WithIndentation_Coverage3(t *testing.T) {
	ac := ArtifactCollection{
		{
			Kind:         ArtifactKindEndpoint,
			Location:     "https://example.com",
			LocationKind: LocationKindRemote,
		},
	}
	result := ac.ToString("  ")
	assert.Contains(t, result, "https://example.com")
}

// ---- Endpoint artifact with discriminator ----

func Test_ArtifactToString_Endpoint_Discriminator_Coverage3(t *testing.T) {
	a := Artifact{
		Kind:         ArtifactKindEndpoint,
		Location:     "https://example.com",
		LocationKind: LocationKindRemote,
		Metadata:     map[string]string{"label": "Primary"},
	}
	result := a.ToString("")
	assert.Contains(t, result, "https://example.com")
	assert.Contains(t, result, "Primary")
}

// ---- mapHostProps coverage ----

func Test_mapHostProps_Coverage3(t *testing.T) {
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

// ---- scaffold.AzureSnakeCase used in mergeDefaultEnvVars ----

func Test_mergeDefaultEnvVars_Coverage3(t *testing.T) {
	// Test that user env overrides defaults
	defaults := map[string]string{
		"PORT": "3000",
		"HOST": "localhost",
	}
	userEnv := []ServiceEnvVar{
		{Name: "PORT", Value: "8080"},
	}

	result := mergeDefaultEnvVars(defaults, userEnv)
	// User env should override default
	found := false
	for _, ev := range result {
		if ev.Name == "PORT" {
			assert.Equal(t, "8080", ev.Value)
			found = true
		}
	}
	assert.True(t, found, "PORT should be in merged result")

	// Default HOST should still be present
	hasHost := false
	for _, ev := range result {
		if ev.Name == "HOST" {
			hasHost = true
		}
	}
	assert.True(t, hasHost, "HOST should be in merged result from defaults")
}

// ---- Additional ToString for Artifact with Discriminator field ----

func Test_ArtifactToString_EndpointMultipleLabels_Coverage3(t *testing.T) {
	artifacts := ArtifactCollection{
		{
			Kind:         ArtifactKindEndpoint,
			Location:     "https://app1.com",
			LocationKind: LocationKindRemote,
			Metadata:     map[string]string{"label": "App 1"},
		},
		{
			Kind:         ArtifactKindEndpoint,
			Location:     "https://app2.com",
			LocationKind: LocationKindRemote,
			Metadata:     map[string]string{"label": "App 2"},
		},
	}
	result := artifacts.ToString("")
	assert.Contains(t, result, "https://app1.com")
	assert.Contains(t, result, "https://app2.com")
	// Both labels should appear
	assert.Contains(t, result, "App 1")
	assert.Contains(t, result, "App 2")
}

// ---- Test ArtifactKind and LocationKind display strings ----

func Test_ArtifactAdd_AllKinds_Coverage3(t *testing.T) {
	kinds := []struct {
		kind ArtifactKind
		loc  string
	}{
		{ArtifactKindEndpoint, "https://example.com"},
		{ArtifactKindContainer, "myimage:latest"},
		{ArtifactKindArchive, "/tmp/app.zip"},
		{ArtifactKindDirectory, "/tmp/output"},
	}

	for _, k := range kinds {
		t.Run(fmt.Sprintf("Add_%s", k.kind), func(t *testing.T) {
			ctx := NewServiceContext()
			err := ctx.Package.Add(&Artifact{
				Kind:         k.kind,
				Location:     k.loc,
				LocationKind: LocationKindLocal,
			})
			require.NoError(t, err)
			assert.Len(t, ctx.Package, 1)
		})
	}
}
