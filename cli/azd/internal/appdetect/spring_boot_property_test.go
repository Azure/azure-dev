package appdetect

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestReadProperties(t *testing.T) {
	var properties = readProperties(filepath.Join("testdata", "java-spring", "project-one"))
	require.Equal(t, "", properties["not.exist"])
	require.Equal(t, "jdbc:h2:mem:testdb", properties["spring.datasource.url"])

	properties = readProperties(filepath.Join("testdata", "java-spring", "project-two"))
	require.Equal(t, "", properties["not.exist"])
	require.Equal(t, "jdbc:h2:mem:testdb", properties["spring.datasource.url"])

	properties = readProperties(filepath.Join("testdata", "java-spring", "project-three"))
	require.Equal(t, "", properties["not.exist"])
	require.Equal(t, "HTML", properties["spring.thymeleaf.mode"])

	properties = readProperties(filepath.Join("testdata", "java-spring", "project-four"))
	require.Equal(t, "", properties["not.exist"])
	require.Equal(t, "mysql", properties["database"])
}

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
		{
			"Has valid environment variable placeholder with default value, but environment variable not set",
			"${VALUE_TWO:defaultValue}",
			map[string]string{},
			"defaultValue",
		},
		{
			"Has valid environment variable placeholder with default value, and environment variable set",
			"${VALUE_THREE:defaultValue}",
			map[string]string{"VALUE_THREE": "valueThree"},
			"valueThree",
		},
		{
			"Has multiple environment variable placeholder with default value, and environment not variable set",
			"jdbc:mysql://${MYSQL_HOST:localhost}:${MYSQL_PORT:3306}/${MYSQL_DATABASE:pet-clinic}",
			map[string]string{},
			"jdbc:mysql://localhost:3306/pet-clinic",
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
