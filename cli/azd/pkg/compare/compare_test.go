package compare

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

func Test_StringUtil_IsStringNilOrEmpty(t *testing.T) {
	tests := []struct {
		name  string
		value *string
		want  bool
	}{
		{
			name:  "nil",
			value: nil,
			want:  true,
		},
		{
			name:  "empty",
			value: convert.RefOf(""),
			want:  true,
		},
		{
			name:  "whitespace",
			value: convert.RefOf("  "),
			want:  true,
		},
		{
			name:  "non-empty",
			value: convert.RefOf("foo"),
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStringNilOrEmpty(tt.value); got != tt.want {
				t.Errorf("IsStringNilOrEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_StringUtil_PtrValueEquals(t *testing.T) {
	tests := []struct {
		name     string
		actual   *string
		expected string
		want     bool
	}{
		{
			name:     "nil",
			actual:   nil,
			expected: "foo",
			want:     false,
		},
		{
			name:     "empty",
			actual:   convert.RefOf(""),
			expected: "foo",
			want:     false,
		},
		{
			name:     "whitespace",
			actual:   convert.RefOf("  "),
			expected: "foo",
			want:     false,
		},
		{
			name:     "non-empty",
			actual:   convert.RefOf("foo"),
			expected: "foo",
			want:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PtrValueEquals(tt.actual, tt.expected); got != tt.want {
				t.Errorf("PtrValueEquals() = %v, want %v", got, tt.want)
			}
		})
	}
}
