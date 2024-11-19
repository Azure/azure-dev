package appdetect

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetEnvironmentVariablePlaceholderHandledValue(t *testing.T) {

	tests := []struct {
		name                 string
		inputValue           string
		environmentVariables map[string]string
		expectedValue        string
	}{
		{
			"No environment variable placeholder",
			"valueOne",
			map[string]string{},
			"valueOne",
		},
		{
			"Has invalid environment variable placeholder",
			"${VALUE_ONE",
			map[string]string{},
			"${VALUE_ONE",
		},
		{
			"Has valid environment variable placeholder, but environment variable not set",
			"${VALUE_TWO}",
			map[string]string{},
			"",
		},
		{
			"Has valid environment variable placeholder, and environment variable set",
			"${VALUE_THREE}",
			map[string]string{"VALUE_THREE": "valueThree"},
			"valueThree",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.environmentVariables {
				err := os.Setenv(k, v)
				require.NoError(t, err)
			}
			handledValue := getEnvironmentVariablePlaceholderHandledValue(tt.inputValue)
			require.Equal(t, tt.expectedValue, handledValue)
		})
	}
}
