package scaffold

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestToResourceConnectionEnv(t *testing.T) {
	tests := []struct {
		name                  string
		inputResourceType     ResourceType
		inputResourceInfoType ResourceInfoType
		want                  string
	}{
		{
			name:                  "mysql username",
			inputResourceType:     ResourceTypeDbMySQL,
			inputResourceInfoType: ResourceInfoTypeUsername,
			want:                  "$resource.connection:db.mysql:username",
		},
		{
			name:                  "postgres password",
			inputResourceType:     ResourceTypeDbPostgres,
			inputResourceInfoType: ResourceInfoTypePassword,
			want:                  "$resource.connection:db.postgres:password",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ToResourceConnectionEnv(tt.inputResourceType, tt.inputResourceInfoType)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func TestIsResourceConnectionEnv(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid",
			input: "$resource.connection:db.postgres:password",
			want:  true,
		},
		{
			name:  "invalid",
			input: "$resource.connection:db.postgres:",
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isResourceConnectionEnv(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestToResourceConnectionInfo(t *testing.T) {
	tests := []struct {
		name                 string
		input                string
		wantResourceType     ResourceType
		wantResourceInfoType ResourceInfoType
	}{
		{
			name:                 "invalid input",
			input:                "$resource.connection:db.mysql::username",
			wantResourceType:     "",
			wantResourceInfoType: "",
		},
		{
			name:                 "mysql username",
			input:                "$resource.connection:db.mysql:username",
			wantResourceType:     ResourceTypeDbMySQL,
			wantResourceInfoType: ResourceInfoTypeUsername,
		},
		{
			name:                 "postgres password",
			input:                "$resource.connection:db.postgres:password",
			wantResourceType:     ResourceTypeDbPostgres,
			wantResourceInfoType: ResourceInfoTypePassword,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceType, resourceInfoType := toResourceConnectionInfo(tt.input)
			assert.Equal(t, tt.wantResourceType, resourceType)
			assert.Equal(t, tt.wantResourceInfoType, resourceInfoType)
		})
	}
}
