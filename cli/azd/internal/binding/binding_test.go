package binding

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeMapWithDuplicationCheck(t *testing.T) {
	empty := map[string]string{}
	name1Value1 := map[string]string{
		"name1": "value1",
	}
	name1Value2 := map[string]string{
		"name1": "value2",
	}
	name2Value2 := map[string]string{
		"name2": "value2",
	}
	name1Value1Name2Value2 := map[string]string{
		"name1": "value1",
		"name2": "value2",
	}

	tests := []struct {
		name          string
		a             map[string]string
		b             map[string]string
		expected      map[string]string
		expectedError error
	}{
		{
			name:          "2 empty map",
			a:             empty,
			b:             empty,
			expected:      empty,
			expectedError: nil,
		},
		{
			name:          "one is empty, another is not",
			a:             empty,
			b:             name1Value1,
			expected:      name1Value1,
			expectedError: nil,
		},
		{
			name:          "no duplication",
			a:             name1Value1,
			b:             name2Value2,
			expected:      name1Value1Name2Value2,
			expectedError: nil,
		},
		{
			name:          "duplicated name but same value",
			a:             name1Value1,
			b:             name1Value1,
			expected:      name1Value1,
			expectedError: nil,
		},
		{
			name:     "duplicated name, different value",
			a:        name1Value1,
			b:        name1Value2,
			expected: nil,
			expectedError: fmt.Errorf("duplicated environment variable. existingValue = %s, newValue = %s",
				"value1", "value2"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := MergeMapWithDuplicationCheck(tt.a, tt.b)
			assert.Equal(t, tt.expected, env)
			assert.Equal(t, tt.expectedError, err)
		})
	}
}

func TestToBindingEnv(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		infoType InfoType
		want     string
	}{
		{
			name:     "postgres password",
			target:   Target{Type: AzureDatabaseForPostgresql},
			infoType: InfoTypePassword,
			want:     "${binding:azure.db.postgresql::password}",
		},
		{
			name:     "mysql username",
			target:   Target{Type: AzureDatabaseForMysql},
			infoType: InfoTypeUsername,
			want:     "${binding:azure.db.mysql::username}",
		},
		{
			name:     "mysql username",
			target:   Target{Type: AzureContainerApp, Name: "testApp"},
			infoType: InfoTypeHost,
			want:     "${binding:azure.host.containerapp:testApp:host}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ToBindingEnv(tt.target, tt.infoType)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func TestIsBindingEnvValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid - whole string",
			input: "${binding:azure.db.postgresql::password}",
			want:  true,
		},
		{
			name:  "valid - sub string",
			input: "optional:configserver:${binding:azure.host.containerapp:testApp:host}?fail-fast=true",
			want:  true,
		},
		{
			name:  "valid - SourceUserAssignedManagedIdentityClientId",
			input: SourceUserAssignedManagedIdentityClientId,
			want:  true,
		},
		{
			name:  "invalid - no target info type",
			input: "${binding:azure.db.postgres::}",
			want:  false,
		},
		{
			name:  "invalid - no required prefix and suffix.",
			input: "binding:azure.db.postgresql::password",
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsBindingEnv(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestToTargetAndInfoType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		target   Target
		infoType InfoType
	}{
		{
			name:     "invalid input",
			input:    "${binding:azure.db.mysql::username}",
			target:   Target{Type: AzureDatabaseForMysql},
			infoType: InfoTypeUsername,
		},
		{
			name:     "postgres password",
			input:    "${binding:azure.db.postgresql::password}",
			target:   Target{Type: AzureDatabaseForPostgresql},
			infoType: InfoTypePassword,
		},
		{
			name:     "mysql username",
			input:    "optional:configserver:${binding:azure.host.containerapp:testApp:host}?fail-fast=true",
			target:   Target{Type: AzureContainerApp, Name: "testApp"},
			infoType: InfoTypeHost,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceType, resourceInfoType := ToTargetAndInfoType(tt.input)
			assert.Equal(t, tt.target, resourceType)
			assert.Equal(t, tt.infoType, resourceInfoType)
		})
	}
}
