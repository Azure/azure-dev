package scaffold

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExpressionParsing(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []expression
		wantErr  bool
	}{
		{
			name:  "simple property reference",
			input: "${.properties.host}",
			expected: []expression{{
				Kind: PropertyExpr,
				Data: PropertyExprData{
					PropertyPath: "properties.host",
				},
			}},
		},
		{
			name:  "simple spec reference",
			input: "${spec.name}",
			expected: []expression{{
				Kind: SpecExpr,
				Data: SpecExprData{
					PropertyPath: "name",
				},
			}},
		},
		{
			name:  "vault reference",
			input: "${vault.SECRET-KEY}",
			expected: []expression{{
				Kind: VaultExpr,
				Data: VaultExprData{
					SecretPath: "SECRET-KEY",
				},
			}},
		},
		{
			name:  "environment variable",
			input: "${DATABASE_URL}",
			expected: []expression{{
				Kind: VarExpr,
				Data: VarExprData{
					Name: "DATABASE_URL",
				},
			}},
		},
		{
			name:  "complex nested expression",
			input: "postgresql://${.properties.user}:${vault.}@${.properties.host}:${DB_PORT}/${spec.name}",
			expected: []expression{
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
			expressions, err := parseExpressions(&tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, len(tt.expected), len(expressions))

			for i, exp := range tt.expected {
				assert.Equal(t, exp.Kind, expressions[i].Kind)
				assert.Equal(t, exp.Data, expressions[i].Data)
			}
		})
	}
}
