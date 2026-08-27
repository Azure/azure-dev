// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateCodeAllowsAdditiveBetaMethod(t *testing.T) {
	t.Parallel()

	stable := map[string]service{
		"ExampleService": {
			name: "ExampleService",
			methods: []method{{
				name:     "Shared",
				kind:     unaryMethod,
				request:  "SharedRequest",
				response: "SharedResponse",
			}},
		},
	}
	beta := map[string]service{
		"ExampleService": {
			name: "ExampleService",
			methods: []method{
				{
					name:     "Shared",
					kind:     unaryMethod,
					request:  "SharedRequest",
					response: "SharedResponse",
				},
				{
					name:     "Preview",
					kind:     unaryMethod,
					request:  "PreviewRequest",
					response: "PreviewResponse",
				},
			},
		},
		"Preview": {
			name: "Preview",
			methods: []method{{
				name:     "OnlyBeta",
				kind:     unaryMethod,
				request:  "PreviewRequest",
				response: "PreviewResponse",
			}},
		},
	}

	generated, err := generateCode(stable, beta)
	require.NoError(t, err)
	_, err = parser.ParseFile(token.NewFileSet(), "generated.go", generated, parser.AllErrors)
	require.NoError(t, err)

	code := string(generated)
	require.Contains(t, code, "type BetaExampleServicePreviewOverride interface")
	require.Contains(t, code, "if override, ok := a.override.(BetaExampleServicePreviewOverride)")
	require.Contains(t, code, "return a.UnimplementedExampleServiceServer.Preview(ctx, req)")
	require.Contains(t, code, "return adaptBetaUnary(")
	require.Contains(t, code, "a.stable.Shared")
	require.Contains(t, code, "v1beta.RegisterPreviewServer")
	require.Contains(t, code, "return a.UnimplementedPreviewServer.OnlyBeta(ctx, req)")
	require.Contains(t, code, "return validateBetaServiceOverride(")
}

func TestGenerateCodeRejectsSharedMethodStreamShapeChange(t *testing.T) {
	t.Parallel()

	stable := map[string]service{
		"ExampleService": {
			name: "ExampleService",
			methods: []method{{
				name:     "Shared",
				kind:     unaryMethod,
				request:  "SharedRequest",
				response: "SharedResponse",
			}},
		},
	}
	beta := map[string]service{
		"ExampleService": {
			name: "ExampleService",
			methods: []method{{
				name:     "Shared",
				kind:     bidiStreamingMethod,
				request:  "SharedRequest",
				response: "SharedResponse",
			}},
		},
	}

	_, err := generateCode(stable, beta)
	require.ErrorContains(t, err, "stream shape differs")
}

func TestGenerateCodeRejectsSharedMessageTypeChange(t *testing.T) {
	t.Parallel()

	stable := map[string]service{
		"ExampleService": {
			name: "ExampleService",
			methods: []method{{
				name:     "Shared",
				kind:     unaryMethod,
				request:  "SharedRequest",
				response: "SharedResponse",
			}},
		},
	}
	beta := map[string]service{
		"ExampleService": {
			name: "ExampleService",
			methods: []method{{
				name:     "Shared",
				kind:     unaryMethod,
				request:  "PreviewRequest",
				response: "SharedResponse",
			}},
		},
	}

	_, err := generateCode(stable, beta)
	require.ErrorContains(t, err, "request or response type differs")
}
