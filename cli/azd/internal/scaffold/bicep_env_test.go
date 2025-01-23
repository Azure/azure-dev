package scaffold

import (
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/binding"
	"github.com/stretchr/testify/assert"
)

func TestToBicepEnv(t *testing.T) {
	tests := []struct {
		name     string
		envName  string
		envValue string
		want     BicepEnv
	}{
		{
			name:     "Plain text",
			envName:  "enable-customer-related-feature",
			envValue: "true",
			want: BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           "enable-customer-related-feature",
				PlainTextValue: "'true'", // Note: Quotation add automatically
			},
		},
		{
			name:     "Plain text which is used for binding, but it's not a binding env",
			envName:  "spring.jms.servicebus.pricing-tier",
			envValue: "premium",
			want: BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           "spring.jms.servicebus.pricing-tier",
				PlainTextValue: "'premium'", // Note: Quotation add automatically
			},
		},
		{
			name:    "Plain text which is a binding env",
			envName: "POSTGRES_PORT",
			envValue: binding.ToBindingEnv(binding.Target{Type: binding.AzureDatabaseForPostgresql},
				binding.InfoTypePort),
			want: BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           "POSTGRES_PORT",
				PlainTextValue: "'5432'",
			},
		},
		{
			name:     "Plain text which is a binding env: SourceUserAssignedManagedIdentityClientId",
			envName:  "spring.cloud.azure.credential.client-id",
			envValue: binding.SourceUserAssignedManagedIdentityClientId,
			want: BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           "spring.cloud.azure.credential.client-id",
				PlainTextValue: placeholderOfSourceClientId,
			},
		},
		{
			name:    "Secret",
			envName: "POSTGRES_PASSWORD",
			envValue: binding.ToBindingEnv(binding.Target{Type: binding.AzureDatabaseForPostgresql},
				binding.InfoTypePassword),
			want: BicepEnv{
				BicepEnvType: BicepEnvTypeSecret,
				Name:         "POSTGRES_PASSWORD",
				SecretName:   "azure-db-postgresql-password",
				SecretValue:  "postgreSqlDatabasePassword",
			},
		},
		{
			name:    "KeuVault Secret",
			envName: "REDIS_PASSWORD",
			envValue: binding.ToBindingEnv(binding.Target{Type: binding.AzureCacheForRedis},
				binding.InfoTypePassword),
			want: BicepEnv{
				BicepEnvType: BicepEnvTypeKeyVaultSecret,
				Name:         "REDIS_PASSWORD",
				SecretName:   "azure-db-redis-password",
				SecretValue:  "redisConn.outputs.keyVaultUrlForPass",
			},
		},
		{
			name:    "Eureka server",
			envName: "eureka.client.serviceUrl.defaultZone",
			envValue: fmt.Sprintf("%s/eureka", binding.ToBindingEnv(binding.Target{
				Type: binding.AzureContainerApp,
				Name: "eurekaServerName",
			}, binding.InfoTypeHost)),
			want: BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           "eureka.client.serviceUrl.defaultZone",
				PlainTextValue: "'https://eurekaServerName.${containerAppsEnvironment.outputs.defaultDomain}/eureka'",
			},
		},
		{
			name:    "Config server",
			envName: "spring.config.import",
			envValue: fmt.Sprintf("optional:configserver:%s?fail-fast=true", binding.ToBindingEnv(binding.Target{
				Type: binding.AzureContainerApp,
				Name: "config-server-name",
			}, binding.InfoTypeHost)),
			want: BicepEnv{
				BicepEnvType: BicepEnvTypePlainText,
				Name:         "spring.config.import",
				PlainTextValue: "'optional:configserver:" +
					"https://configServerName.${containerAppsEnvironment.outputs.defaultDomain}?fail-fast=true'",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := ToBicepEnv(tt.envName, tt.envValue)
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
