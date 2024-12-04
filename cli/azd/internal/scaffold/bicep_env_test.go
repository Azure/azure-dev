package scaffold

import (
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestToBicepEnv(t *testing.T) {
	tests := []struct {
		name string
		in   Env
		want BicepEnv
	}{
		{
			name: "Plain text",
			in: Env{
				Name:  "enable-customer-related-feature",
				Value: "true",
			},
			want: BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           "enable-customer-related-feature",
				PlainTextValue: "'true'", // Note: Quotation add automatically
			},
		},
		{
			name: "Plain text from EnvTypeResourceConnectionPlainText",
			in: Env{
				Name:  "spring.jms.servicebus.pricing-tier",
				Value: "premium",
			},
			want: BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           "spring.jms.servicebus.pricing-tier",
				PlainTextValue: "'premium'", // Note: Quotation add automatically
			},
		},
		{
			name: "Plain text from EnvTypeResourceConnectionResourceInfo",
			in: Env{
				Name:  "POSTGRES_PORT",
				Value: ToResourceConnectionEnv(ResourceTypeDbPostgres, ResourceInfoTypePort),
			},
			want: BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           "POSTGRES_PORT",
				PlainTextValue: "'5432'",
			},
		},
		{
			name: "Secret",
			in: Env{
				Name:  "POSTGRES_PASSWORD",
				Value: ToResourceConnectionEnv(ResourceTypeDbPostgres, ResourceInfoTypePassword),
			},
			want: BicepEnv{
				BicepEnvType: BicepEnvTypeSecret,
				Name:         "POSTGRES_PASSWORD",
				SecretName:   "db-postgres-password",
				SecretValue:  "postgreSqlDatabasePassword",
			},
		},
		{
			name: "KeuVault Secret",
			in: Env{
				Name:  "REDIS_PASSWORD",
				Value: ToResourceConnectionEnv(ResourceTypeDbRedis, ResourceInfoTypePassword),
			},
			want: BicepEnv{
				BicepEnvType: BicepEnvTypeKeyVaultSecret,
				Name:         "REDIS_PASSWORD",
				SecretName:   "db-redis-password",
				SecretValue:  "redisConn.outputs.keyVaultUrlForPass",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ToBicepEnv(tt.in)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func TestToBicepEnvPlainTextValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "string",
			in:   "inputStringExample",
			want: "'inputStringExample'",
		},
		{
			name: "single variable",
			in:   "${inputSingleVariableExample}",
			want: "inputSingleVariableExample",
		},
		{
			name: "multiple variable",
			in:   "${HOST}:${PORT}",
			want: "'${HOST}:${PORT}'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := toBicepEnvPlainTextValue(tt.in)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func TestShouldAddToBicepFile(t *testing.T) {
	tests := []struct {
		name         string
		infraSpec    ServiceSpec
		propertyName string
		want         bool
	}{
		{
			name:         "not related property and not using mysql and postgres",
			infraSpec:    ServiceSpec{},
			propertyName: "test",
			want:         true,
		},
		{
			name:         "not using mysql and postgres",
			infraSpec:    ServiceSpec{},
			propertyName: "spring.datasource.url",
			want:         true,
		},
		{
			name: "not using user assigned managed identity",
			infraSpec: ServiceSpec{
				DbMySql: &DatabaseMySql{
					AuthType: internal.AuthTypePassword,
				},
			},
			propertyName: "spring.datasource.url",
			want:         true,
		},
		{
			name: "not service connector added property",
			infraSpec: ServiceSpec{
				DbMySql: &DatabaseMySql{
					AuthType: internal.AuthTypePassword,
				},
			},
			propertyName: "test",
			want:         true,
		},
		{
			name: "should not added",
			infraSpec: ServiceSpec{
				DbMySql: &DatabaseMySql{
					AuthType: internal.AuthTypePassword,
				},
			},
			propertyName: "spring.datasource.url",
			want:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ShouldAddToBicepFile(tt.infraSpec, tt.propertyName)
			assert.Equal(t, tt.want, actual)
		})
	}
}
