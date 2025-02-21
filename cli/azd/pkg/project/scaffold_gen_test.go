// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

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

func Test_WithResolvedDependencies(t *testing.T) {
	tests := []struct {
		name      string
		resources map[string]*ResourceConfig
		want      map[string]*ResourceConfig
	}{
		{
			name:      "empty resources",
			resources: map[string]*ResourceConfig{},
			want:      map[string]*ResourceConfig{},
		},
		{
			name: "resource with no dependencies",
			resources: map[string]*ResourceConfig{
				"app": {Name: "app", Type: ResourceTypeHostContainerApp},
			},
			want: map[string]*ResourceConfig{
				"app": {Name: "app", Type: ResourceTypeHostContainerApp},
			},
		},
		{
			name: "mongodb requires keyvault",
			resources: map[string]*ResourceConfig{
				"mongodb": {Name: "mongodb", Type: ResourceTypeDbMongo},
			},
			want: map[string]*ResourceConfig{
				"mongodb":   {Name: "mongodb", Type: ResourceTypeDbMongo},
				"key-vault": {Name: "key-vault", Type: ResourceTypeKeyVault},
			},
		},
		{
			name: "redis requires keyvault",
			resources: map[string]*ResourceConfig{
				"redis": {Name: "redis", Type: ResourceTypeDbRedis},
			},
			want: map[string]*ResourceConfig{
				"redis":     {Name: "redis", Type: ResourceTypeDbRedis},
				"key-vault": {Name: "key-vault", Type: ResourceTypeKeyVault},
			},
		},
		{
			name: "multiple resources sharing keyvault dependency",
			resources: map[string]*ResourceConfig{
				"mongodb": {Name: "mongodb", Type: ResourceTypeDbMongo},
				"redis":   {Name: "redis", Type: ResourceTypeDbRedis},
			},
			want: map[string]*ResourceConfig{
				"mongodb":   {Name: "mongodb", Type: ResourceTypeDbMongo},
				"redis":     {Name: "redis", Type: ResourceTypeDbRedis},
				"key-vault": {Name: "key-vault", Type: ResourceTypeKeyVault},
			},
		},
		{
			name: "dependency already present",
			resources: map[string]*ResourceConfig{
				"mongodb":   {Name: "mongodb", Type: ResourceTypeDbMongo},
				"key-vault": {Name: "key-vault", Type: ResourceTypeKeyVault},
			},
			want: map[string]*ResourceConfig{
				"mongodb":   {Name: "mongodb", Type: ResourceTypeDbMongo},
				"key-vault": {Name: "key-vault", Type: ResourceTypeKeyVault},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithResolvedDependencies(tt.resources)

			// Check if got and want have same length
			if len(got) != len(tt.want) {
				t.Errorf("WithResolvedDependencies() got %v resources, want %v", len(got), len(tt.want))
			}

			// Check if all resources in want exist in got with same properties
			for name, wantRes := range tt.want {
				gotRes, exists := got[name]
				if !exists {
					t.Errorf("WithResolvedDependencies() missing resource %v", name)
					continue
				}
				if gotRes.Name != wantRes.Name {
					t.Errorf("WithResolvedDependencies() resource %v got name %v, want %v", name, gotRes.Name, wantRes.Name)
				}
				if gotRes.Type != wantRes.Type {
					t.Errorf("WithResolvedDependencies() resource %v got type %v, want %v", name, gotRes.Type, wantRes.Type)
				}
			}
		})
	}
}
