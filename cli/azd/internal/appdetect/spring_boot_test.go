package appdetect

import (
	"testing"
)

func TestGetDatabaseName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"jdbc:postgresql://localhost:5432/your-database-name", "your-database-name"},
		{"jdbc:postgresql://remote_host:5432/your-database-name", "your-database-name"},
		{"jdbc:postgresql://your_postgresql_server:5432/your-database-name?sslmode=require", "your-database-name"},
		{
			"jdbc:postgresql://your_postgresql_server.postgres.database.azure.com:5432/your-database-name?sslmode=require",
			"your-database-name",
		},
		{
			"jdbc:postgresql://your_postgresql_server:5432/your-database-name?user=your_username&password=your_password",
			"your-database-name",
		},
		{
			"jdbc:postgresql://your_postgresql_server.postgres.database.azure.com:5432/your-database-name" +
				"?sslmode=require&spring.datasource.azure.passwordless-enabled=true", "your-database-name",
		},
	}
	for _, test := range tests {
		result := getDatabaseName(test.input)
		if result != test.expected {
			t.Errorf("For input '%s', expected '%s', but got '%s'", test.input, test.expected, result)
		}
	}
}

func TestIsValidDatabaseName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"InvalidNameWithUnderscore", "invalid_name", false},
		{"TooShortName", "sh", false},
		{
			"TooLongName", "this-name-is-way-too-long-to-be-considered-valid-" +
				"because-it-exceeds-sixty-three-characters", false,
		},
		{"InvalidStartWithHyphen", "-invalid-start", false},
		{"InvalidEndWithHyphen", "invalid-end-", false},
		{"ValidName", "valid-name", true},
		{"ValidNameWithNumbers", "valid123-name", true},
		{"ValidNameWithOnlyLetters", "valid-name", true},
		{"ValidNameWithOnlyNumbers", "123456", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IsValidDatabaseName(test.input)
			if result != test.expected {
				t.Errorf("For input '%s', expected %v, but got %v", test.input, test.expected, result)
			}
		})
	}
}
