// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExpressionParsing(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []Expression
		wantErr  bool
	}{
		{
			name:  "simple property reference",
			input: "${.properties.host}",
			expected: []Expression{{
				Kind: PropertyExpr,
				Data: PropertyExprData{
					PropertyPath: "properties.host",
				},
			}},
		},
		{
			name:  "simple spec reference",
			input: "${spec.name}",
			expected: []Expression{{
				Kind: SpecExpr,
				Data: SpecExprData{
					PropertyPath: "name",
				},
			}},
		},
		{
			name:  "vault reference",
			input: "${vault.SECRET-KEY}",
			expected: []Expression{{
				Kind: VaultExpr,
				Data: VaultExprData{
					SecretPath: "SECRET-KEY",
				},
			}},
		},
		{
			name:  "environment variable",
			input: "${DATABASE_URL}",
			expected: []Expression{{
				Kind: VarExpr,
				Data: VarExprData{
					Name: "DATABASE_URL",
				},
			}},
		},
		{
			name:  "func",
			input: "${func .id spec.name name}",
			expected: []Expression{{
				Kind: FuncExpr,
				Data: FuncExprData{
					FuncName: "func",
					Args: []*Expression{
						{
							Kind: PropertyExpr,
							Data: PropertyExprData{PropertyPath: "id"},
						},
						{
							Kind: SpecExpr,
							Data: SpecExprData{PropertyPath: "name"},
						},
						{
							Kind: VarExpr,
							Data: VarExprData{Name: "name"},
						},
					},
				},
			}},
		},
		{
			name:  "complex nested expression",
			input: "postgresql://${.properties.user}:${vault.}@${.properties.host}:${DB_PORT}/${spec.name}",
			expected: []Expression{
				{
					Kind: PropertyExpr,
					Data: PropertyExprData{PropertyPath: "properties.user"},
				},
				{
					Kind: VaultExpr,
					Data: VaultExprData{SecretPath: ""},
				},
				{
					Kind: PropertyExpr,
					Data: PropertyExprData{PropertyPath: "properties.host"},
				},
				{
					Kind: VarExpr,
					Data: VarExprData{Name: "DB_PORT"},
				},
				{
					Kind: SpecExpr,
					Data: SpecExprData{PropertyPath: "name"},
				},
			},
		},
		{
			name:    "invalid token type",
			input:   "${invalid.key}",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expressions, err := Parse(&tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, len(tt.expected), len(expressions))

			for i, exp := range tt.expected {
				assert.Equal(t, exp.Kind, expressions[i].Kind)

				if exp.Kind == FuncExpr {
					expectedFunc := exp.Data.(FuncExprData)
					actualFunc := expressions[i].Data.(FuncExprData)
					assert.Equal(t, expectedFunc.FuncName, actualFunc.FuncName)
					assert.Equal(t, len(expectedFunc.Args), len(actualFunc.Args))

					for j, arg := range expectedFunc.Args {
						assert.Equal(t, arg.Kind, actualFunc.Args[j].Kind)
						assert.Equal(t, arg.Data, actualFunc.Args[j].Data)
					}
				} else {
					assert.Equal(t, exp.Data, expressions[i].Data)
				}
			}
		})
	}
}
