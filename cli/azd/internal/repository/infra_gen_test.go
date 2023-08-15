package repository

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
			actual := containerAppName(tt.in)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func Test_bicepName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"uppercase separators", "this-is-my-var-123", "thisIsMyVar123"},
		{"allowed characters", "myVar_!#%^", "myVar"},
		{"normalize casing", "MyVar", "myVar"},
		{"dash at front or end", "--my-var--", "myVar"},
		{"multiple dashes", "my----var", "myVar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := bicepName(tt.in)
			assert.Equal(t, tt.want, actual)
		})
	}
}
