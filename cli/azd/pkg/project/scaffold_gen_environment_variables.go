package project

import (
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"strings"
)

func getResourceConnectionEnvs(usedResource *ResourceConfig,
	infraSpec *scaffold.InfraSpec) ([]scaffold.Env, error) {
	resourceType := usedResource.Type
	authType, err := getAuthType(infraSpec, usedResource.Type)
	if err != nil {
		return []scaffold.Env{}, err
	}
	switch resourceType {
	case ResourceTypeDbPostgres:
		switch authType {
		case internal.AuthTypePassword:
			return []scaffold.Env{
				{
					Name: "POSTGRES_USERNAME",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeUsername),
				},
				{
					Name: "POSTGRES_PASSWORD",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypePassword),
				},
				{
					Name: "POSTGRES_HOST",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeHost),
				},
				{
					Name: "POSTGRES_DATABASE",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeDatabaseName),
				},
				{
					Name: "POSTGRES_PORT",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypePort),
				},
				{
					Name: "POSTGRES_URL",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeUrl),
				},
				{
					Name: "spring.datasource.url",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeJdbcUrl),
				},
				{
					Name: "spring.datasource.username",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeUsername),
				},
				{
					Name: "spring.datasource.password",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypePassword),
				},
			}, nil
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []scaffold.Env{
				{
					Name: "POSTGRES_USERNAME",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeUsername),
				},
				{
					Name: "POSTGRES_HOST",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeHost),
				},
				{
					Name: "POSTGRES_DATABASE",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeDatabaseName),
				},
				{
					Name: "POSTGRES_PORT",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypePort),
				},
				{
					Name: "spring.datasource.url",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeJdbcUrl),
				},
				{
					Name: "spring.datasource.username",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbPostgres, scaffold.ResourceInfoTypeUsername),
				},
				{
					Name:  "spring.datasource.azure.passwordless-enabled",
					Value: "true",
				},
			}, nil
		default:
			return []scaffold.Env{}, unsupportedAuthTypeError(resourceType, authType)
		}
	case ResourceTypeDbMySQL:
		switch authType {
		case internal.AuthTypePassword:
			return []scaffold.Env{
				{
					Name: "MYSQL_USERNAME",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeUsername),
				},
				{
					Name: "MYSQL_PASSWORD",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypePassword),
				},
				{
					Name: "MYSQL_HOST",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeHost),
				},
				{
					Name: "MYSQL_DATABASE",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeDatabaseName),
				},
				{
					Name: "MYSQL_PORT",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypePort),
				},
				{
					Name: "MYSQL_URL",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeUrl),
				},
				{
					Name: "spring.datasource.url",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeJdbcUrl),
				},
				{
					Name: "spring.datasource.username",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeUsername),
				},
				{
					Name: "spring.datasource.password",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypePassword),
				},
			}, nil
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []scaffold.Env{
				{
					Name: "MYSQL_USERNAME",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeUsername),
				},
				{
					Name: "MYSQL_HOST",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeHost),
				},
				{
					Name: "MYSQL_PORT",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypePort),
				},
				{
					Name: "MYSQL_DATABASE",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeDatabaseName),
				},
				{
					Name: "spring.datasource.url",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeJdbcUrl),
				},
				{
					Name: "spring.datasource.username",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMySQL, scaffold.ResourceInfoTypeUsername),
				},
				{
					Name:  "spring.datasource.azure.passwordless-enabled",
					Value: "true",
				},
			}, nil
		default:
			return []scaffold.Env{}, unsupportedAuthTypeError(resourceType, authType)
		}
	case ResourceTypeDbRedis:
		switch authType {
		case internal.AuthTypePassword:
			return []scaffold.Env{
				{
					Name: "REDIS_HOST",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbRedis, scaffold.ResourceInfoTypeHost),
				},
				{
					Name: "REDIS_PORT",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbRedis, scaffold.ResourceInfoTypePort),
				},
				{
					Name: "REDIS_ENDPOINT",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbRedis, scaffold.ResourceInfoTypeEndpoint),
				},
				{
					Name: "REDIS_URL",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbRedis, scaffold.ResourceInfoTypeUrl),
				},
				{
					Name: "REDIS_PASSWORD",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbRedis, scaffold.ResourceInfoTypePassword),
				},
				{
					Name: "spring.data.redis.url",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbRedis, scaffold.ResourceInfoTypeUrl),
				},
			}, nil
		default:
			return []scaffold.Env{}, unsupportedAuthTypeError(resourceType, authType)
		}
	case ResourceTypeDbMongo:
		switch authType {
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []scaffold.Env{
				{
					Name: "MONGODB_URL",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMongo, scaffold.ResourceInfoTypeUrl),
				},
				{
					Name: "spring.data.mongodb.uri",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMongo, scaffold.ResourceInfoTypeUrl),
				},
				{
					Name: "spring.data.mongodb.database",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbMongo, scaffold.ResourceInfoTypeDatabaseName),
				},
			}, nil
		default:
			return []scaffold.Env{}, unsupportedAuthTypeError(resourceType, authType)
		}
	case ResourceTypeDbCosmos:
		switch authType {
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []scaffold.Env{
				{
					Name: "spring.cloud.azure.cosmos.endpoint",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbCosmos, scaffold.ResourceInfoTypeEndpoint),
				},
				{
					Name: "spring.cloud.azure.cosmos.database",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeDbCosmos, scaffold.ResourceInfoTypeDatabaseName),
				},
			}, nil
		default:
			return []scaffold.Env{}, unsupportedAuthTypeError(resourceType, authType)
		}
	case ResourceTypeMessagingServiceBus:
		if infraSpec.AzureServiceBus.IsJms {
			switch authType {
			case internal.AuthTypeUserAssignedManagedIdentity:
				return []scaffold.Env{
					{
						Name:  "spring.jms.servicebus.pricing-tier",
						Value: "premium",
					},
					{
						Name:  "spring.jms.servicebus.passwordless-enabled",
						Value: "true",
					},
					{
						Name:  "spring.jms.servicebus.credential.managed-identity-enabled",
						Value: "true",
					},
					{
						Name:  "spring.jms.servicebus.credential.client-id",
						Value: scaffold.PlaceHolderForServiceIdentityClientId(),
					},
					{
						Name: "spring.jms.servicebus.namespace",
						Value: scaffold.ToResourceConnectionEnv(
							scaffold.ResourceTypeMessagingServiceBus, scaffold.ResourceInfoTypeNamespace),
					},
					{
						Name:  "spring.jms.servicebus.connection-string",
						Value: "",
					},
				}, nil
			case internal.AuthTypeConnectionString:
				return []scaffold.Env{
					{
						Name:  "spring.jms.servicebus.pricing-tier",
						Value: "premium",
					},
					{
						Name: "spring.jms.servicebus.connection-string",
						Value: scaffold.ToResourceConnectionEnv(
							scaffold.ResourceTypeMessagingServiceBus, scaffold.ResourceInfoTypeConnectionString),
					},
					{
						Name:  "spring.jms.servicebus.passwordless-enabled",
						Value: "false",
					},
					{
						Name:  "spring.jms.servicebus.credential.managed-identity-enabled",
						Value: "false",
					},
					{
						Name:  "spring.jms.servicebus.credential.client-id",
						Value: "",
					},
					{
						Name:  "spring.jms.servicebus.namespace",
						Value: "",
					},
				}, nil
			default:
				return []scaffold.Env{}, unsupportedResourceTypeError(resourceType)
			}
		} else {
			// service bus, not jms
			switch authType {
			case internal.AuthTypeUserAssignedManagedIdentity:
				return []scaffold.Env{
					// Not add this: spring.cloud.azure.servicebus.connection-string = ""
					// because of this: https://github.com/Azure/azure-sdk-for-java/issues/42880
					{
						Name:  "spring.cloud.azure.servicebus.credential.managed-identity-enabled",
						Value: "true",
					},
					{
						Name:  "spring.cloud.azure.servicebus.credential.client-id",
						Value: scaffold.PlaceHolderForServiceIdentityClientId(),
					},
					{
						Name: "spring.cloud.azure.servicebus.namespace",
						Value: scaffold.ToResourceConnectionEnv(
							scaffold.ResourceTypeMessagingServiceBus, scaffold.ResourceInfoTypeNamespace),
					},
				}, nil
			case internal.AuthTypeConnectionString:
				return []scaffold.Env{
					{
						Name: "spring.cloud.azure.servicebus.namespace",
						Value: scaffold.ToResourceConnectionEnv(
							scaffold.ResourceTypeMessagingServiceBus, scaffold.ResourceInfoTypeNamespace),
					},
					{
						Name: "spring.cloud.azure.servicebus.connection-string",
						Value: scaffold.ToResourceConnectionEnv(
							scaffold.ResourceTypeMessagingServiceBus, scaffold.ResourceInfoTypeConnectionString),
					},
					{
						Name:  "spring.cloud.azure.servicebus.credential.managed-identity-enabled",
						Value: "false",
					},
					{
						Name:  "spring.cloud.azure.servicebus.credential.client-id",
						Value: "",
					},
				}, nil
			default:
				return []scaffold.Env{}, unsupportedResourceTypeError(resourceType)
			}
		}
	case ResourceTypeMessagingKafka:
		// event hubs for kafka
		var springBootVersionDecidedInformation []scaffold.Env
		if strings.HasPrefix(infraSpec.AzureEventHubs.SpringBootVersion, "2.") {
			springBootVersionDecidedInformation = []scaffold.Env{
				{
					Name:  "spring.cloud.stream.binders.kafka.environment.spring.main.sources",
					Value: "com.azure.spring.cloud.autoconfigure.eventhubs.kafka.AzureEventHubsKafkaAutoConfiguration",
				},
			}
		} else {
			springBootVersionDecidedInformation = []scaffold.Env{
				{
					Name: "spring.cloud.stream.binders.kafka.environment.spring.main.sources",
					Value: "com.azure.spring.cloud.autoconfigure.implementation.eventhubs.kafka" +
						".AzureEventHubsKafkaAutoConfiguration",
				},
			}
		}
		var commonInformation []scaffold.Env
		switch authType {
		case internal.AuthTypeUserAssignedManagedIdentity:
			commonInformation = []scaffold.Env{
				// Not add this: spring.cloud.azure.eventhubs.connection-string = ""
				// because of this: https://github.com/Azure/azure-sdk-for-java/issues/42880
				{
					Name: "spring.cloud.stream.kafka.binder.brokers",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeMessagingKafka, scaffold.ResourceInfoTypeEndpoint),
				},
				{
					Name:  "spring.cloud.azure.eventhubs.credential.managed-identity-enabled",
					Value: "true",
				},
				{
					Name:  "spring.cloud.azure.eventhubs.credential.client-id",
					Value: scaffold.PlaceHolderForServiceIdentityClientId(),
				},
			}
		case internal.AuthTypeConnectionString:
			commonInformation = []scaffold.Env{
				{
					Name: "spring.cloud.stream.kafka.binder.brokers",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeMessagingKafka, scaffold.ResourceInfoTypeEndpoint),
				},
				{
					Name: "spring.cloud.azure.eventhubs.connection-string",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeMessagingKafka, scaffold.ResourceInfoTypeConnectionString),
				},
				{
					Name:  "spring.cloud.azure.eventhubs.credential.managed-identity-enabled",
					Value: "false",
				},
				{
					Name:  "spring.cloud.azure.eventhubs.credential.client-id",
					Value: "",
				},
			}
		default:
			return []scaffold.Env{}, unsupportedAuthTypeError(resourceType, authType)
		}
		return mergeEnvWithDuplicationCheck(springBootVersionDecidedInformation, commonInformation)
	case ResourceTypeMessagingEventHubs:
		switch authType {
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []scaffold.Env{
				// Not add this: spring.cloud.azure.eventhubs.connection-string = ""
				// because of this: https://github.com/Azure/azure-sdk-for-java/issues/42880
				{
					Name:  "spring.cloud.azure.eventhubs.credential.managed-identity-enabled",
					Value: "true",
				},
				{
					Name:  "spring.cloud.azure.eventhubs.credential.client-id",
					Value: scaffold.PlaceHolderForServiceIdentityClientId(),
				},
				{
					Name: "spring.cloud.azure.eventhubs.namespace",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeMessagingEventHubs, scaffold.ResourceInfoTypeNamespace),
				},
			}, nil
		case internal.AuthTypeConnectionString:
			return []scaffold.Env{
				{
					Name: "spring.cloud.azure.eventhubs.namespace",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeMessagingEventHubs, scaffold.ResourceInfoTypeNamespace),
				},
				{
					Name: "spring.cloud.azure.eventhubs.connection-string",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeMessagingEventHubs, scaffold.ResourceInfoTypeConnectionString),
				},
				{
					Name:  "spring.cloud.azure.eventhubs.credential.managed-identity-enabled",
					Value: "false",
				},
				{
					Name:  "spring.cloud.azure.eventhubs.credential.client-id",
					Value: "",
				},
			}, nil
		default:
			return []scaffold.Env{}, unsupportedResourceTypeError(resourceType)
		}
	case ResourceTypeStorage:
		switch authType {
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []scaffold.Env{
				{
					Name: "spring.cloud.azure.eventhubs.processor.checkpoint-store.account-name",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeStorage, scaffold.ResourceInfoTypeAccountName),
				},
				{
					Name:  "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.managed-identity-enabled",
					Value: "true",
				},
				{
					Name:  "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.client-id",
					Value: scaffold.PlaceHolderForServiceIdentityClientId(),
				},
				{
					Name:  "spring.cloud.azure.eventhubs.processor.checkpoint-store.connection-string",
					Value: "",
				},
			}, nil
		case internal.AuthTypeConnectionString:
			return []scaffold.Env{
				{
					Name: "spring.cloud.azure.eventhubs.processor.checkpoint-store.account-name",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeStorage, scaffold.ResourceInfoTypeAccountName),
				},
				{
					Name: "spring.cloud.azure.eventhubs.processor.checkpoint-store.connection-string",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeStorage, scaffold.ResourceInfoTypeConnectionString),
				},
				{
					Name:  "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.managed-identity-enabled",
					Value: "false",
				},
				{
					Name:  "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.client-id",
					Value: "",
				},
			}, nil
		default:
			return []scaffold.Env{}, unsupportedResourceTypeError(resourceType)
		}
	case ResourceTypeOpenAiModel:
		switch authType {
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []scaffold.Env{
				{
					Name: "AZURE_OPENAI_ENDPOINT",
					Value: scaffold.ToResourceConnectionEnv(
						scaffold.ResourceTypeOpenAiModel, scaffold.ResourceInfoTypeEndpoint),
				},
			}, nil
		default:
			return []scaffold.Env{}, unsupportedResourceTypeError(resourceType)
		}
	case ResourceTypeHostContainerApp: // todo improve this and delete Frontend and Backend in scaffold.ServiceSpec
		switch authType {
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []scaffold.Env{}, nil
		default:
			return []scaffold.Env{}, unsupportedAuthTypeError(resourceType, authType)
		}
	default:
		return []scaffold.Env{}, unsupportedResourceTypeError(resourceType)
	}
}

func unsupportedResourceTypeError(resourceType ResourceType) error {
	return fmt.Errorf("unsupported resource type, resourceType = %s", resourceType)
}

func unsupportedAuthTypeError(resourceType ResourceType, authType internal.AuthType) error {
	return fmt.Errorf("unsupported auth type, resourceType = %s, authType = %s", resourceType, authType)
}

func mergeEnvWithDuplicationCheck(a []scaffold.Env,
	b []scaffold.Env) ([]scaffold.Env, error) {
	ab := append(a, b...)
	var result []scaffold.Env
	seenName := make(map[string]scaffold.Env)
	for _, value := range ab {
		if existingValue, exist := seenName[value.Name]; exist {
			if value != existingValue {
				return []scaffold.Env{}, duplicatedEnvError(existingValue, value)
			}
		} else {
			seenName[value.Name] = value
			result = append(result, value)
		}
	}
	return result, nil
}

func addNewEnvironmentVariable(serviceSpec *scaffold.ServiceSpec, name string, value string) error {
	merged, err := mergeEnvWithDuplicationCheck(serviceSpec.Envs,
		[]scaffold.Env{
			{
				Name:  name,
				Value: value,
			},
		},
	)
	if err != nil {
		return err
	}
	serviceSpec.Envs = merged
	return nil
}

func duplicatedEnvError(existingValue scaffold.Env, newValue scaffold.Env) error {
	return fmt.Errorf("duplicated environment variable. existingValue = %s, newValue = %s",
		existingValue, newValue)
}
