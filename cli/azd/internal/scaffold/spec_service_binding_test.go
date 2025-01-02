package scaffold

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToServiceBindingEnvName(t *testing.T) {
	tests := []struct {
		name                  string
		inputResourceType     ServiceType
		inputResourceInfoType ServiceBindingInfoType
		want                  string
	}{
		{
			name:                  "mysql username",
			inputResourceType:     ServiceTypeDbMySQL,
			inputResourceInfoType: ServiceBindingInfoTypeUsername,
			want:                  "$service.binding:db.mysql:username",
		},
		{
			name:                  "postgres password",
			inputResourceType:     ServiceTypeDbPostgres,
			inputResourceInfoType: ServiceBindingInfoTypePassword,
			want:                  "$service.binding:db.postgres:password",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ToServiceBindingEnvValue(tt.inputResourceType, tt.inputResourceInfoType)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func TestIsServiceBindingEnvName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid",
			input: "$service.binding:db.postgres:password",
			want:  true,
		},
		{
			name:  "invalid",
			input: "$service.binding:db.postgres:",
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isServiceBindingEnvValue(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestToServiceTypeAndServiceBindingInfoType(t *testing.T) {
	tests := []struct {
		name                 string
		input                string
		wantResourceType     ServiceType
		wantResourceInfoType ServiceBindingInfoType
	}{
		{
			name:                 "invalid input",
			input:                "$service.binding:db.mysql::username",
			wantResourceType:     "",
			wantResourceInfoType: "",
		},
		{
			name:                 "mysql username",
			input:                "$service.binding:db.mysql:username",
			wantResourceType:     ServiceTypeDbMySQL,
			wantResourceInfoType: ServiceBindingInfoTypeUsername,
		},
		{
			name:                 "postgres password",
			input:                "$service.binding:db.postgres:password",
			wantResourceType:     ServiceTypeDbPostgres,
			wantResourceInfoType: ServiceBindingInfoTypePassword,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceType, resourceInfoType := toServiceTypeAndServiceBindingInfoType(tt.input)
			assert.Equal(t, tt.wantResourceType, resourceType)
			assert.Equal(t, tt.wantResourceInfoType, resourceInfoType)
		})
	}
}

func TestMergeEnvWithDuplicationCheck(t *testing.T) {
	var empty []Env
	name1Value1 := []Env{
		{
			Name:  "name1",
			Value: "value1",
		},
	}
	name1Value2 := []Env{
		{
			Name:  "name1",
			Value: "value2",
		},
	}
	name2Value2 := []Env{
		{
			Name:  "name2",
			Value: "value2",
		},
	}
	name1Value1Name2Value2 := []Env{
		{
			Name:  "name1",
			Value: "value1",
		},
		{
			Name:  "name2",
			Value: "value2",
		},
	}

	tests := []struct {
		name      string
		a         []Env
		b         []Env
		wantEnv   []Env
		wantError error
	}{
		{
			name:      "2 empty array",
			a:         empty,
			b:         empty,
			wantEnv:   empty,
			wantError: nil,
		},
		{
			name:      "one is empty, another is not",
			a:         empty,
			b:         name1Value1,
			wantEnv:   name1Value1,
			wantError: nil,
		},
		{
			name:      "no duplication",
			a:         name1Value1,
			b:         name2Value2,
			wantEnv:   name1Value1Name2Value2,
			wantError: nil,
		},
		{
			name:      "duplicated name but same value",
			a:         name1Value1,
			b:         name1Value1,
			wantEnv:   name1Value1,
			wantError: nil,
		},
		{
			name:    "duplicated name, different value",
			a:       name1Value1,
			b:       name1Value2,
			wantEnv: []Env{},
			wantError: fmt.Errorf("duplicated environment variable. existingValue = %s, newValue = %s",
				name1Value1[0], name1Value2[0]),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := mergeEnvWithDuplicationCheck(tt.a, tt.b)
			assert.Equal(t, tt.wantEnv, env)
			assert.Equal(t, tt.wantError, err)
		})
	}
}
