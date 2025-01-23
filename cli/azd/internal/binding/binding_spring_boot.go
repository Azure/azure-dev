package binding

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
)

const (
	IsSpringBootJms   MetadataType = "IsSpringBootJms"
	IsSpringBootKafka MetadataType = "IsSpringBootKafka"
	SpringBootVersion MetadataType = "SpringBootVersion"
)

func GetBindingEnvsForSpringBoot(source Source, target Target) (map[string]string, error) {
	switch target.Type {
	case AzureDatabaseForPostgresql:
		return GetBindingEnvsForSpringBootToPostgresql(target.AuthType)
	case AzureDatabaseForMysql:
		return GetBindingEnvsForSpringBootToMysql(target.AuthType)
	case AzureCosmosDBForMongoDB:
		return GetBindingEnvsForSpringBootToMongoDb(target.AuthType)
	case AzureCosmosDBForNoSQL:
		return GetBindingEnvsForSpringBootToCosmosNoSQL(target.AuthType)
	case AzureCacheForRedis:
		return GetBindingEnvsForSpringBootToRedis(target.AuthType)
	case AzureServiceBus:
		if source.Metadata[IsSpringBootJms] == "true" {
			return GetBindingEnvsForSpringBootToServiceBusJms(target.AuthType)
		} else {
			return GetBindingEnvsForSpringBootToServiceBusNotJms(target.AuthType)
		}
	case AzureEventHubs:
		if source.Metadata[IsSpringBootKafka] == "true" {
			return GetBindingEnvsForSpringBootToEventHubsKafka(source.Metadata[SpringBootVersion], target.AuthType)
		} else {
			return GetServiceBindingEnvsForEventHubs(target.AuthType)
		}
	case AzureStorageAccount:
		return GetServiceBindingEnvsForStorageAccount(target.AuthType)
	default:
		return nil, fmt.Errorf("unsupported target type when binding for spring boot app, target.Type = %s",
			target.Type)
	}
}

func GetBindingEnvsForSpringBootToPostgresql(authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureDatabaseForPostgresql}
	switch authType {
	case internal.AuthTypePassword:
		return map[string]string{
			"spring.datasource.url":      ToBindingEnv(target, InfoTypeJdbcUrl),
			"spring.datasource.username": ToBindingEnv(target, InfoTypeUsername),
			"spring.datasource.password": ToBindingEnv(target, InfoTypePassword),
		}, nil
	case internal.AuthTypeUserAssignedManagedIdentity:
		return map[string]string{
			"spring.datasource.url":                                  ToBindingEnv(target, InfoTypeJdbcUrl),
			"spring.datasource.username":                             ToBindingEnv(target, InfoTypeUsername),
			"spring.datasource.password":                             "",
			"spring.datasource.azure.passwordless-enabled":           "true",
			"spring.cloud.azure.credential.client-id":                SourceUserAssignedManagedIdentityClientId,
			"spring.cloud.azure.credential.managed-identity-enabled": "true",
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureDatabaseForPostgresql, authType)
	}
}

func GetBindingEnvsForSpringBootToMysql(authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureDatabaseForMysql}
	switch authType {
	case internal.AuthTypePassword:
		return map[string]string{
			"spring.datasource.url":      ToBindingEnv(target, InfoTypeJdbcUrl),
			"spring.datasource.username": ToBindingEnv(target, InfoTypeUsername),
			"spring.datasource.password": ToBindingEnv(target, InfoTypePassword),
		}, nil
	case internal.AuthTypeUserAssignedManagedIdentity:
		return map[string]string{
			"spring.datasource.url":                                  ToBindingEnv(target, InfoTypeJdbcUrl),
			"spring.datasource.username":                             ToBindingEnv(target, InfoTypeUsername),
			"spring.datasource.password":                             "",
			"spring.datasource.azure.passwordless-enabled":           "true",
			"spring.cloud.azure.credential.client-id":                SourceUserAssignedManagedIdentityClientId,
			"spring.cloud.azure.credential.managed-identity-enabled": "true",
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureDatabaseForMysql, authType)
	}
}

func GetBindingEnvsForSpringBootToMongoDb(authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureCosmosDBForMongoDB}
	switch authType {
	case internal.AuthTypeConnectionString:
		return map[string]string{
			"spring.data.mongodb.uri":      ToBindingEnv(target, InfoTypeJdbcUrl),
			"spring.data.mongodb.database": ToBindingEnv(target, InfoTypeDatabaseName),
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureCosmosDBForMongoDB, authType)
	}
}

func GetBindingEnvsForSpringBootToCosmosNoSQL(authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureCosmosDBForNoSQL}
	switch authType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		return map[string]string{
			"spring.cloud.azure.cosmos.endpoint": ToBindingEnv(target, InfoTypeEndpoint),
			"spring.cloud.azure.cosmos.database": ToBindingEnv(target, InfoTypeDatabaseName),
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureCosmosDBForNoSQL, authType)
	}
}

func GetBindingEnvsForSpringBootToRedis(authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureCacheForRedis}
	switch authType {
	case internal.AuthTypePassword:
		return map[string]string{
			"spring.data.redis.url": ToBindingEnv(target, InfoTypeUrl),
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureCacheForRedis, authType)
	}
}

func GetBindingEnvsForSpringBootToServiceBusJms(authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureServiceBus}
	switch authType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		return map[string]string{
			"spring.jms.servicebus.pricing-tier":                        "premium",
			"spring.jms.servicebus.passwordless-enabled":                "true",
			"spring.jms.servicebus.credential.managed-identity-enabled": "true",
			"spring.jms.servicebus.credential.client-id":                SourceUserAssignedManagedIdentityClientId,
			"spring.jms.servicebus.namespace":                           ToBindingEnv(target, InfoTypeNamespace),
			"spring.jms.servicebus.connection-string":                   "",
		}, nil
	case internal.AuthTypeConnectionString:
		return map[string]string{
			"spring.jms.servicebus.pricing-tier":                        "premium",
			"spring.jms.servicebus.passwordless-enabled":                "false",
			"spring.jms.servicebus.credential.managed-identity-enabled": "false",
			"spring.jms.servicebus.credential.client-id":                "",
			"spring.jms.servicebus.namespace":                           "",
			"spring.jms.servicebus.connection-string":                   ToBindingEnv(target, InfoTypeConnectionString),
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureServiceBus, authType)
	}
}

func GetBindingEnvsForSpringBootToServiceBusNotJms(authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureServiceBus}
	switch authType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		return map[string]string{
			// Not add this: spring.cloud.azure.servicebus.connection-string = ""
			// because of this: https://github.com/Azure/azure-sdk-for-java/issues/42880
			"spring.cloud.azure.servicebus.credential.managed-identity-enabled": "true",
			"spring.cloud.azure.servicebus.credential.client-id":                SourceUserAssignedManagedIdentityClientId,
			"spring.cloud.azure.servicebus.namespace": ToBindingEnv(target,
				InfoTypeNamespace),
		}, nil
	case internal.AuthTypeConnectionString:
		return map[string]string{
			"spring.cloud.azure.servicebus.namespace": ToBindingEnv(target,
				InfoTypeNamespace),
			"spring.cloud.azure.servicebus.connection-string": ToBindingEnv(target,
				InfoTypeConnectionString),
			"spring.cloud.azure.servicebus.credential.managed-identity-enabled": "false",
			"spring.cloud.azure.servicebus.credential.client-id":                "",
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureServiceBus, authType)
	}
}

func GetBindingEnvsForSpringBootToEventHubsKafka(springBootVersion string,
	authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureEventHubs}
	var springBootVersionDecidedBindingEnv = make(map[string]string)
	if strings.HasPrefix(springBootVersion, "2.") {
		springBootVersionDecidedBindingEnv["spring.cloud.stream.binders.kafka.environment.spring.main.sources"] =
			"com.azure.spring.cloud.autoconfigure.eventhubs.kafka.AzureEventHubsKafkaAutoConfiguration"
	} else {
		springBootVersionDecidedBindingEnv["spring.cloud.stream.binders.kafka.environment.spring.main.sources"] =
			"com.azure.spring.cloud.autoconfigure.implementation.eventhubs.kafka" +
				".AzureEventHubsKafkaAutoConfiguration"
	}
	var commonInformation map[string]string
	switch authType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		commonInformation = map[string]string{
			// Not add this: spring.cloud.azure.servicebus.connection-string = ""
			// because of this: https://github.com/Azure/azure-sdk-for-java/issues/42880
			"spring.cloud.stream.kafka.binder.brokers":                         ToBindingEnv(target, InfoTypeEndpoint),
			"spring.cloud.azure.eventhubs.credential.managed-identity-enabled": "true",
			"spring.cloud.azure.eventhubs.credential.client-id":                SourceUserAssignedManagedIdentityClientId,
		}
	case internal.AuthTypeConnectionString:
		commonInformation = map[string]string{
			"spring.cloud.stream.kafka.binder.brokers": ToBindingEnv(target, InfoTypeEndpoint),
			"spring.cloud.azure.eventhubs.connection-string": ToBindingEnv(target,
				InfoTypeConnectionString),
			"spring.cloud.azure.eventhubs.credential.managed-identity-enabled": "false",
			"spring.cloud.azure.eventhubs.credential.client-id":                "",
		}
	default:
		return nil, unsupportedAuthTypeError(AzureEventHubs, authType)
	}
	return MergeMapWithDuplicationCheck(springBootVersionDecidedBindingEnv, commonInformation)
}

func GetServiceBindingEnvsForEventHubs(authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureEventHubs}
	switch authType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		return map[string]string{
			// Not add this: spring.cloud.azure.eventhubs.connection-string = ""
			// because of this: https://github.com/Azure/azure-sdk-for-java/issues/42880
			"spring.cloud.azure.eventhubs.credential.managed-identity-enabled": "true",
			"spring.cloud.azure.eventhubs.credential.client-id":                SourceUserAssignedManagedIdentityClientId,
			"spring.cloud.azure.eventhubs.namespace":                           ToBindingEnv(target, InfoTypeNamespace),
		}, nil
	case internal.AuthTypeConnectionString:
		return map[string]string{
			"spring.cloud.azure.eventhubs.namespace": ToBindingEnv(target, InfoTypeNamespace),
			"spring.cloud.azure.eventhubs.connection-string": ToBindingEnv(target,
				InfoTypeConnectionString),
			"spring.cloud.azure.eventhubs.credential.managed-identity-enabled": "false",
			"spring.cloud.azure.eventhubs.credential.client-id":                "",
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureEventHubs, authType)
	}
}

func GetServiceBindingEnvsForStorageAccount(authType internal.AuthType) (map[string]string, error) {
	target := Target{Type: AzureStorageAccount}
	switch authType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		return map[string]string{
			"spring.cloud.azure.eventhubs.processor.checkpoint-store.account-name": ToBindingEnv(
				target, InfoTypeAccountName),
			"spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.managed-identity-enabled": "true",
			"spring.cloud.azure.eventhubs.processor.checkpoint-store.credential." +
				"client-id": SourceUserAssignedManagedIdentityClientId,
			"spring.cloud.azure.eventhubs.processor.checkpoint-store.connection-string": "",
		}, nil
	case internal.AuthTypeConnectionString:
		return map[string]string{
			"spring.cloud.azure.eventhubs.processor.checkpoint-store.account-name": ToBindingEnv(
				target, InfoTypeAccountName),
			"spring.cloud.azure.eventhubs.processor.checkpoint-store.connection-string": ToBindingEnv(
				target, InfoTypeConnectionString),
			"spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.managed-identity-enabled": "false",
			"spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.client-id":                "",
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureStorageAccount, authType)
	}
}

func GetServiceBindingEnvsForEurekaServer(eurekaServerName string) map[string]string {
	return map[string]string{
		"eureka.client.register-with-eureka": "true",
		"eureka.client.fetch-registry":       "true",
		"eureka.instance.prefer-ip-address":  "true",
		"eureka.client.serviceUrl.defaultZone": fmt.Sprintf("%s/eureka",
			ToBindingEnv(Target{Type: AzureContainerApp, Name: eurekaServerName}, InfoTypeHost)),
	}
}

func GetServiceBindingEnvsForConfigServer(configServerName string) map[string]string {
	return map[string]string{
		"spring.config.import": fmt.Sprintf("optional:configserver:%s?fail-fast=true",
			ToBindingEnv(Target{Type: AzureContainerApp, Name: configServerName}, InfoTypeHost)),
	}
}
