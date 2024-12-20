package scaffold

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypePort),
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
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypePassword),
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
				Value: ToServiceBindingEnvValue(ServiceTypeDbRedis, ServiceBindingInfoTypePassword),
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
