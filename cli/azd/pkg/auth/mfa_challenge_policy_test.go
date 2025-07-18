package auth

import (
	"reflect"
	"testing"
)

func TestParseWwwAuthenticateHeader(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []AuthChallenge
	}{
		{
			name:  "Basic with realm",
			input: `Basic realm="example.com"`,
			expected: []AuthChallenge{
				{
					Scheme:     "Basic",
					AuthParams: map[string]string{"realm": "example.com"},
				},
			},
		},
		{
			name:  "Bearer with token68",
			input: `Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9`,
			expected: []AuthChallenge{
				{
					Scheme:  "Bearer",
					Token68: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
				},
			},
		},
		{
			name:  "Bearer with parameters",
			input: `Bearer realm="api", error="invalid_token"`,
			expected: []AuthChallenge{
				{
					Scheme: "Bearer",
					AuthParams: map[string]string{
						"realm": "api",
						"error": "invalid_token",
					},
				},
			},
		},
		{
			name:  "Multiple schemes",
			input: `Basic realm="test", Digest realm="other", nonce="abc123"`,
			expected: []AuthChallenge{
				{
					Scheme:     "Basic",
					AuthParams: map[string]string{"realm": "test"},
				},
				{
					Scheme: "Digest",
					AuthParams: map[string]string{
						"realm": "other",
						"nonce": "abc123",
					},
				},
			},
		},
		{
			name:  "Digest with multiple parameters",
			input: `Digest realm="example.com", qop="auth,auth-int", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093"`,
			expected: []AuthChallenge{
				{
					Scheme: "Digest",
					AuthParams: map[string]string{
						"realm": "example.com",
						"qop":   "auth,auth-int",
						"nonce": "dcd98b7102dd2f0e8b11d0f600bfb0c093",
					},
				},
			},
		},
		{
			name:  "Bearer with error description",
			input: `Bearer error="invalid_token", error_description="The access token expired"`,
			expected: []AuthChallenge{
				{
					Scheme: "Bearer",
					AuthParams: map[string]string{
						"error":             "invalid_token",
						"error_description": "The access token expired",
					},
				},
			},
		},
		{
			name:  "Mixed schemes with token68 and parameters",
			input: `Bearer error="invalid_token", Basic realm="fallback"`,
			expected: []AuthChallenge{
				{
					Scheme: "Bearer",
					AuthParams: map[string]string{
						"error": "invalid_token",
					},
				},
				{
					Scheme:     "Basic",
					AuthParams: map[string]string{"realm": "fallback"},
				},
			},
		},
		{
			name:  "Quoted values with spaces",
			input: `Basic realm="test with spaces"`,
			expected: []AuthChallenge{
				{
					Scheme:     "Basic",
					AuthParams: map[string]string{"realm": "test with spaces"},
				},
			},
		},
		{
			name:  "Escaped quotes",
			input: `Basic realm="test with \"quotes\""`,
			expected: []AuthChallenge{
				{
					Scheme:     "Basic",
					AuthParams: map[string]string{"realm": `test with "quotes"`},
				},
			},
		},
		{
			name:     "Empty header",
			input:    ``,
			expected: []AuthChallenge{},
		},
		{
			name:     "Whitespace only",
			input:    `   `,
			expected: []AuthChallenge{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseWwwAuthenticateHeader(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d challenges, got %d", len(tt.expected), len(result))
				return
			}

			for i, expected := range tt.expected {
				actual := result[i]

				if actual.Scheme != expected.Scheme {
					t.Errorf("challenge %d: expected scheme %q, got %q", i, expected.Scheme, actual.Scheme)
				}

				if actual.Token68 != expected.Token68 {
					t.Errorf("challenge %d: expected token68 %q, got %q", i, expected.Token68, actual.Token68)
				}

				if !reflect.DeepEqual(actual.AuthParams, expected.AuthParams) {
					t.Errorf("challenge %d: expected params %v, got %v", i, expected.AuthParams, actual.AuthParams)
				}
			}
		})
	}
}

func TestParseCommaElement(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected element
	}{
		{
			name:     "Bearer with parameter",
			input:    `Bearer realm="example.com"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "Bearer with token68",
			input:    `Bearer eyJhbGciOaJIUzI1NiJ9`,
			expected: element{Scheme: "Bearer", Value: "eyJhbGciOaJIUzI1NiJ9"},
		},
		{
			name:     "parameter only",
			input:    `error="invalid_token"`,
			expected: element{Key: "error", Value: "invalid_token"},
		},
		// BWS (Bad White Space) tests - RFC 7230 allows optional whitespace around =
		{
			name:     "BWS around equals",
			input:    `Bearer realm = "example.com"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "BWS before equals only",
			input:    `Bearer realm ="example.com"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "BWS after equals only",
			input:    `Bearer realm= "example.com"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "multiple spaces around equals",
			input:    `Bearer realm  =  "example.com"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "tabs around equals",
			input:    "Bearer realm\t=\t\"example.com\"",
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "mixed whitespace around equals",
			input:    "Bearer realm \t= \t\"example.com\"",
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		// Leading/trailing whitespace tests
		{
			name:     "leading whitespace",
			input:    `   Bearer realm="example.com"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "leading tabs",
			input:    "\t\tBearer realm=\"example.com\"",
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "mixed leading whitespace",
			input:    " \t Bearer realm=\"example.com\"",
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		// Whitespace between scheme and parameter
		{
			name:     "multiple spaces between scheme and param",
			input:    `Bearer   realm="example.com"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "tab between scheme and param",
			input:    "Bearer\trealm=\"example.com\"",
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		{
			name:     "mixed whitespace between scheme and param",
			input:    "Bearer \t realm=\"example.com\"",
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		// Unquoted values with whitespace
		{
			name:     "unquoted value with trailing space",
			input:    `Bearer realm=example.com `,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example.com"},
		},
		// Token68 with whitespace
		{
			name:     "token68 with extra spaces",
			input:    `Bearer   eyJhbGciOaJIUzI1NiJ9`,
			expected: element{Scheme: "Bearer", Value: "eyJhbGciOaJIUzI1NiJ9"},
		},
		{
			name:     "token68 with tabs",
			input:    "Bearer\t\teyJhbGciOaJIUzI1NiJ9",
			expected: element{Scheme: "Bearer", Value: "eyJhbGciOaJIUzI1NiJ9"},
		},
		// Quoted strings with internal whitespace preservation
		{
			name:     "quoted value with internal spaces",
			input:    `Bearer realm="example with spaces.com"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example with spaces.com"},
		},
		{
			name:     "quoted value with internal tabs",
			input:    "Bearer realm=\"example\twith\ttabs.com\"",
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example\twith\ttabs.com"},
		},
		// Escaped quotes
		{
			name:     "escaped quotes in value",
			input:    `Bearer realm="example \"quoted\" value"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example \"quoted\" value"},
		},
		{
			name:     "escaped backslash",
			input:    `Bearer realm="example\\value"`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example\\value"},
		},
		// Edge cases
		{
			name:     "empty quoted value",
			input:    `Bearer realm=""`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: ""},
		},
		{
			name:     "parameter with no value",
			input:    `Bearer realm=`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: ""},
		},
		// Malformed cases that should still parse reasonably
		{
			name:     "missing quotes around spaced value",
			input:    `Bearer realm=example with spaces`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "example with spaces"},
		},
		{
			name:     "unclosed quote",
			input:    `Bearer realm="unclosed`,
			expected: element{Scheme: "Bearer", Key: "realm", Value: "\"unclosed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			elem, err := parseCommaElement(tt.input)

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if elem.Scheme != tt.expected.Scheme {
				t.Errorf("expected scheme %q, got %q", tt.expected.Scheme, elem.Scheme)
			}

			if elem.Key != tt.expected.Key {
				t.Errorf("expected key %q, got %q", tt.expected.Key, elem.Key)
			}

			if elem.Value != tt.expected.Value {
				t.Errorf("expected value %q, got %q", tt.expected.Value, elem.Value)
			}
		})
	}
}
