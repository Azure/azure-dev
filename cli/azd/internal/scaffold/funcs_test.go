package scaffold

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_containerAppName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"allowed characters", "MyApp_!#%^", "myapp"},
		{"dash at front or end", "-my-app-", "my-app"},
		{"multiple dashes", "my----app", "my-app"},
		{"at length", "123456789app", "123456789app"},
		{"over length", "123456789my-app", "123456789my"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := containerAppName(tt.in, 12)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func Test_BicepName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"alpha upper snake", "THIS_IS_MY_VAR_123", "thisIsMyVar123"},
		{"uppercase separators", "this-is-my-var-123", "thisIsMyVar123"},
		{"allowed characters", "myVar_!#%^", "myVar"},
		{"normalize casing", "MyVar", "myVar"},
		{"dash at front or end", "--my-var--", "myVar"},
		{"multiple dashes", "my----var", "myVar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := BicepName(tt.in)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func Test_AlphaUpperSnake(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"uppercase separators", "this-is-my-var-123", "THIS_IS_MY_VAR_123"},
		{"allowed characters", "myVar!#%^", "MYVAR"},
		{"dash at front or end", "--my-var--", "MY_VAR"},
		{"multiple dashes", "my----var", "MY_VAR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := AlphaSnakeUpper(tt.in)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func Test_EnvFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no uppercase letters",
			input:    "myenv",
			expected: "${AZURE_MYENV}",
		},
		{
			name:     "single uppercase letter",
			input:    "myEnv",
			expected: "${AZURE_MY_ENV}",
		},
		{
			name:     "multiple uppercase letters",
			input:    "myEnvFormat",
			expected: "${AZURE_MY_ENV_FORMAT}",
		},
		{
			name:     "uppercase letters at the beginning",
			input:    "EnvFormat",
			expected: "${AZURE_ENV_FORMAT}",
		},
		{
			name:     "uppercase letters at the end",
			input:    "envFormaT",
			expected: "${AZURE_ENV_FORMA_T}",
		},
		{
			name:     "uppercase letters in the middle",
			input:    "envFormatString",
			expected: "${AZURE_ENV_FORMAT_STRING}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := EnvFormat(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
