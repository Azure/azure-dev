// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractInvokeExample(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "content-level example wins over schema",
			spec: `{
				"paths": {
					"/invocations": {
						"post": {
							"requestBody": {
								"content": {
									"application/json": {
										"example": {"message": "hi"},
										"schema": {
											"type": "object",
											"example": {"never": "used"}
										}
									}
								}
							}
						}
					}
				}
			}`,
			want: `{"message":"hi"}`,
		},
		{
			name: "schema-level example used when content example missing",
			spec: `{
				"paths": {
					"/invocations": {
						"post": {
							"requestBody": {
								"content": {
									"application/json": {
										"schema": {
											"type": "object",
											"example": {"q": "Hello"}
										}
									}
								}
							}
						}
					}
				}
			}`,
			want: `{"q":"Hello"}`,
		},
		{
			name: "generated from required+properties[*].example",
			spec: `{
				"paths": {
					"/invocations": {
						"post": {
							"requestBody": {
								"content": {
									"application/json": {
										"schema": {
											"type": "object",
											"required": ["message", "tone"],
											"properties": {
												"message": {"type": "string", "example": "Hello"},
												"tone":    {"type": "string", "example": "friendly"},
												"unused":  {"type": "string", "example": "skip"}
											}
										}
									}
								}
							}
						}
					}
				}
			}`,
			want: `{"message":"Hello","tone":"friendly"}`,
		},
		{
			name: "$ref under requestBody returns empty (out of scope)",
			spec: `{
				"paths": {
					"/invocations": {
						"post": {
							"requestBody": {"$ref": "#/components/requestBodies/Invoke"}
						}
					}
				}
			}`,
			want: "",
		},
		{
			name: "$ref under schema returns empty (out of scope)",
			spec: `{
				"paths": {
					"/invocations": {
						"post": {
							"requestBody": {
								"content": {
									"application/json": {
										"schema": {"$ref": "#/components/schemas/InvokeRequest"}
									}
								}
							}
						}
					}
				}
			}`,
			want: "",
		},
		{
			name: "missing /invocations path returns empty",
			spec: `{"paths": {"/health": {"get": {}}}}`,
			want: "",
		},
		{
			name: "malformed JSON returns empty",
			spec: `not json at all`,
			want: "",
		},
		{
			name: "empty spec returns empty",
			spec: ``,
			want: "",
		},
		{
			name: "required without example produces empty",
			spec: `{
				"paths": {
					"/invocations": {
						"post": {
							"requestBody": {
								"content": {
									"application/json": {
										"schema": {
											"type": "object",
											"required": ["message"],
											"properties": {
												"message": {"type": "string"}
											}
										}
									}
								}
							}
						}
					}
				}
			}`,
			want: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractInvokeExample([]byte(tt.spec))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReadCachedOpenAPISpec(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	specBytes := []byte(`{"openapi":"3.0.0"}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "openapi-echo-local.json"), specBytes, 0o600))

	t.Run("returns bytes when file exists", func(t *testing.T) {
		t.Parallel()
		got, err := ReadCachedOpenAPISpec(dir, "echo", "local")
		require.NoError(t, err)
		assert.Equal(t, specBytes, got)
	})

	t.Run("missing file yields nil,nil (not an error)", func(t *testing.T) {
		t.Parallel()
		got, err := ReadCachedOpenAPISpec(dir, "echo", "remote")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("path separators in agent name are sanitized to match writer", func(t *testing.T) {
		t.Parallel()
		// Writer (helpers.go fetchOpenAPISpec) replaces "/", "\", and ".." with "_".
		require.NoError(t, os.WriteFile(filepath.Join(dir, "openapi-evil_x-local.json"), specBytes, 0o600))
		got, err := ReadCachedOpenAPISpec(dir, "evil/x", "local")
		require.NoError(t, err)
		assert.Equal(t, specBytes, got)
	})

	t.Run("missing inputs yield a typed error", func(t *testing.T) {
		t.Parallel()
		_, err := ReadCachedOpenAPISpec("", "echo", "local")
		assert.Error(t, err)
		_, err = ReadCachedOpenAPISpec(dir, "", "local")
		assert.Error(t, err)
		_, err = ReadCachedOpenAPISpec(dir, "echo", "")
		assert.Error(t, err)
	})
}

func TestSanitizeAgentName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"echo":        "echo",
		"evil/path":   "evil_path",
		"evil\\path":  "evil_path",
		"../trav":     "__trav",
		"my agent":    "my agent",
		"a/b\\c/../d": "a_b_c___d",
	}
	for input, want := range cases {
		assert.Equal(t, want, sanitizeAgentName(input), "input=%q", input)
	}
}
