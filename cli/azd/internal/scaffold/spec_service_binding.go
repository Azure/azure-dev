package scaffold

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
)

// todo merge ServiceType and project.ResourceType
// Not use project.ResourceType because it will cause cycle import.
// Not merge it in current PR to avoid conflict with upstream main branch.
// Solution proposal: define a ServiceType in lower level that can be used both in scaffold and project package.

type ServiceType string

const (
	ServiceTypeDbRedis             ServiceType = "db.redis"
	ServiceTypeDbPostgres          ServiceType = "db.postgres"
	ServiceTypeDbMySQL             ServiceType = "db.mysql"
	ServiceTypeDbMongo             ServiceType = "db.mongo"
	ServiceTypeDbCosmos            ServiceType = "db.cosmos"
	ServiceTypeHostContainerApp    ServiceType = "host.containerapp"
	ServiceTypeOpenAiModel         ServiceType = "ai.openai.model"
	ServiceTypeMessagingServiceBus ServiceType = "messaging.servicebus"
	ServiceTypeMessagingEventHubs  ServiceType = "messaging.eventhubs"
	ServiceTypeStorage             ServiceType = "storage"
)

type ServiceBindingInfoType string

const (
	ServiceBindingInfoTypeHost             ServiceBindingInfoType = "host"
	ServiceBindingInfoTypePort             ServiceBindingInfoType = "port"
	ServiceBindingInfoTypeEndpoint         ServiceBindingInfoType = "endpoint"
	ServiceBindingInfoTypeDatabaseName     ServiceBindingInfoType = "databaseName"
	ServiceBindingInfoTypeNamespace        ServiceBindingInfoType = "namespace"
	ServiceBindingInfoTypeAccountName      ServiceBindingInfoType = "accountName"
	ServiceBindingInfoTypeUsername         ServiceBindingInfoType = "username"
	ServiceBindingInfoTypePassword         ServiceBindingInfoType = "password"
	ServiceBindingInfoTypeUrl              ServiceBindingInfoType = "url"
	ServiceBindingInfoTypeJdbcUrl          ServiceBindingInfoType = "jdbcUrl"
	ServiceBindingInfoTypeConnectionString ServiceBindingInfoType = "connectionString"
)

var serviceBindingEnvValuePrefix = "$service.binding"

func isServiceBindingEnvValue(env string) bool {
	if !strings.HasPrefix(env, serviceBindingEnvValuePrefix) {
		return false
	}
	a := strings.Split(env, ":")
	if len(a) != 3 {
		return false
	}
	return a[0] != "" && a[1] != "" && a[2] != ""
}

func ToServiceBindingEnvValue(resourceType ServiceType, resourceInfoType ServiceBindingInfoType) string {
	return fmt.Sprintf("%s:%s:%s", serviceBindingEnvValuePrefix, resourceType, resourceInfoType)
}

func toServiceTypeAndServiceBindingInfoType(resourceConnectionEnv string) (
	serviceType ServiceType, infoType ServiceBindingInfoType) {
	if !isServiceBindingEnvValue(resourceConnectionEnv) {
		return "", ""
	}
	a := strings.Split(resourceConnectionEnv, ":")
	return ServiceType(a[1]), ServiceBindingInfoType(a[2])
}

func BindToPostgres(serviceSpec *ServiceSpec, postgres *DatabasePostgres) error {
	serviceSpec.DbPostgres = postgres
	envs, err := GetServiceBindingEnvsForPostgres(*postgres)
	if err != nil {
		return err
	}
	serviceSpec.Envs, err = mergeEnvWithDuplicationCheck(serviceSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

func BindToMySql(serviceSpec *ServiceSpec, mysql *DatabaseMySql) error {
	serviceSpec.DbMySql = mysql
	envs, err := GetServiceBindingEnvsForMysql(*mysql)
	if err != nil {
		return err
	}
	serviceSpec.Envs, err = mergeEnvWithDuplicationCheck(serviceSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

func BindToMongoDb(serviceSpec *ServiceSpec, mongo *DatabaseCosmosMongo) error {
	serviceSpec.DbCosmosMongo = mongo
	envs := GetServiceBindingEnvsForMongo()
	var err error
	serviceSpec.Envs, err = mergeEnvWithDuplicationCheck(serviceSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

func BindToCosmosDb(serviceSpec *ServiceSpec, cosmos *DatabaseCosmosAccount) error {
	serviceSpec.DbCosmos = cosmos
	envs := GetServiceBindingEnvsForCosmos()
	var err error
	serviceSpec.Envs, err = mergeEnvWithDuplicationCheck(serviceSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

func BindToRedis(serviceSpec *ServiceSpec, redis *DatabaseRedis) error {
	serviceSpec.DbRedis = redis
	envs := GetServiceBindingEnvsForRedis()
	var err error
	serviceSpec.Envs, err = mergeEnvWithDuplicationCheck(serviceSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

func BindToServiceBus(serviceSpec *ServiceSpec, serviceBus *AzureDepServiceBus) error {
	serviceSpec.AzureServiceBus = serviceBus
	envs, err := GetServiceBindingEnvsForServiceBus(*serviceBus)
	if err != nil {
		return err
	}
	serviceSpec.Envs, err = mergeEnvWithDuplicationCheck(serviceSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

func BindToEventHubs(serviceSpec *ServiceSpec, eventHubs *AzureDepEventHubs) error {
	serviceSpec.AzureEventHubs = eventHubs
	envs, err := GetServiceBindingEnvsForEventHubs(*eventHubs)
	if err != nil {
		return err
	}
	serviceSpec.Envs, err = mergeEnvWithDuplicationCheck(serviceSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

func BindToStorageAccount(serviceSpec *ServiceSpec, account *AzureDepStorageAccount) error {
	serviceSpec.AzureStorageAccount = account
	envs, err := GetServiceBindingEnvsForStorageAccount(*account)
	if err != nil {
		return err
	}
	serviceSpec.Envs, err = mergeEnvWithDuplicationCheck(serviceSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

func BindToAIModels(serviceSpec *ServiceSpec, model string) error {
	serviceSpec.AIModels = append(serviceSpec.AIModels, AIModelReference{Name: model})
	envs := GetServiceBindingEnvsForAIModel()
	var err error
	serviceSpec.Envs, err = mergeEnvWithDuplicationCheck(serviceSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

// BindToContainerApp a call b
// todo:
//  1. Add field in ServiceSpec to identify b's app type like Eureka server and Config server.
//  2. Create GetServiceBindingEnvsForContainerApp
//  3. Merge GetServiceBindingEnvsForEurekaServer and GetServiceBindingEnvsForConfigServer into
//     GetServiceBindingEnvsForContainerApp.
//  4. Delete printHintsAboutUseHostContainerApp use GetServiceBindingEnvsForContainerApp instead
func BindToContainerApp(a *ServiceSpec, b *ServiceSpec) {
	if a.Frontend == nil {
		a.Frontend = &Frontend{}
	}
	a.Frontend.Backends = append(a.Frontend.Backends, ServiceReference{Name: b.Name})
	if b.Backend == nil {
		b.Backend = &Backend{}
	}
	b.Backend.Frontends = append(b.Backend.Frontends, ServiceReference{Name: b.Name})
}

func GetServiceBindingEnvsForPostgres(postgres DatabasePostgres) ([]Env, error) {
	switch postgres.AuthType {
	case internal.AuthTypePassword:
		return []Env{
			{
				Name:  "POSTGRES_USERNAME",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeUsername),
			},
			{
				Name:  "POSTGRES_PASSWORD",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypePassword),
			},
			{
				Name:  "POSTGRES_HOST",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeHost),
			},
			{
				Name:  "POSTGRES_DATABASE",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeDatabaseName),
			},
			{
				Name:  "POSTGRES_PORT",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypePort),
			},
			{
				Name:  "POSTGRES_URL",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeUrl),
			},
			{
				Name:  "spring.datasource.url",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeJdbcUrl),
			},
			{
				Name:  "spring.datasource.username",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeUsername),
			},
			{
				Name:  "spring.datasource.password",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypePassword),
			},
		}, nil
	case internal.AuthTypeUserAssignedManagedIdentity:
		return []Env{
			{
				Name:  "POSTGRES_USERNAME",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeUsername),
			},
			{
				Name:  "POSTGRES_HOST",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeHost),
			},
			{
				Name:  "POSTGRES_DATABASE",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeDatabaseName),
			},
			{
				Name:  "POSTGRES_PORT",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypePort),
			},
			{
				Name:  "spring.datasource.url",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeJdbcUrl),
			},
			{
				Name:  "spring.datasource.username",
				Value: ToServiceBindingEnvValue(ServiceTypeDbPostgres, ServiceBindingInfoTypeUsername),
			},
			{
				Name:  "spring.datasource.azure.passwordless-enabled",
				Value: "true",
			},
		}, nil
	default:
		return []Env{}, unsupportedAuthTypeError(ServiceTypeDbPostgres, postgres.AuthType)
	}
}

func GetServiceBindingEnvsForMysql(mysql DatabaseMySql) ([]Env, error) {
	switch mysql.AuthType {
	case internal.AuthTypePassword:
		return []Env{
			{
				Name:  "MYSQL_USERNAME",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeUsername),
			},
			{
				Name:  "MYSQL_PASSWORD",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypePassword),
			},
			{
				Name:  "MYSQL_HOST",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeHost),
			},
			{
				Name:  "MYSQL_DATABASE",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeDatabaseName),
			},
			{
				Name:  "MYSQL_PORT",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypePort),
			},
			{
				Name:  "MYSQL_URL",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeUrl),
			},
			{
				Name:  "spring.datasource.url",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeJdbcUrl),
			},
			{
				Name:  "spring.datasource.username",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeUsername),
			},
			{
				Name:  "spring.datasource.password",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypePassword),
			},
		}, nil
	case internal.AuthTypeUserAssignedManagedIdentity:
		return []Env{
			{
				Name:  "MYSQL_USERNAME",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeUsername),
			},
			{
				Name:  "MYSQL_HOST",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeHost),
			},
			{
				Name:  "MYSQL_PORT",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypePort),
			},
			{
				Name:  "MYSQL_DATABASE",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeDatabaseName),
			},
			{
				Name:  "spring.datasource.url",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeJdbcUrl),
			},
			{
				Name:  "spring.datasource.username",
				Value: ToServiceBindingEnvValue(ServiceTypeDbMySQL, ServiceBindingInfoTypeUsername),
			},
			{
				Name:  "spring.datasource.azure.passwordless-enabled",
				Value: "true",
			},
		}, nil
	default:
		return []Env{}, unsupportedAuthTypeError(ServiceTypeDbMySQL, mysql.AuthType)
	}
}

func GetServiceBindingEnvsForMongo() []Env {
	return []Env{
		{
			Name:  "MONGODB_URL",
			Value: ToServiceBindingEnvValue(ServiceTypeDbMongo, ServiceBindingInfoTypeUrl),
		},
		{
			Name:  "spring.data.mongodb.uri",
			Value: ToServiceBindingEnvValue(ServiceTypeDbMongo, ServiceBindingInfoTypeUrl),
		},
		{
			Name:  "spring.data.mongodb.database",
			Value: ToServiceBindingEnvValue(ServiceTypeDbMongo, ServiceBindingInfoTypeDatabaseName),
		},
	}
}

func GetServiceBindingEnvsForCosmos() []Env {
	return []Env{
		{
			Name: "spring.cloud.azure.cosmos.endpoint",
			Value: ToServiceBindingEnvValue(
				ServiceTypeDbCosmos, ServiceBindingInfoTypeEndpoint),
		},
		{
			Name: "spring.cloud.azure.cosmos.database",
			Value: ToServiceBindingEnvValue(
				ServiceTypeDbCosmos, ServiceBindingInfoTypeDatabaseName),
		},
	}
}

func GetServiceBindingEnvsForRedis() []Env {
	return []Env{
		{
			Name: "REDIS_HOST",
			Value: ToServiceBindingEnvValue(
				ServiceTypeDbRedis, ServiceBindingInfoTypeHost),
		},
		{
			Name: "REDIS_PORT",
			Value: ToServiceBindingEnvValue(
				ServiceTypeDbRedis, ServiceBindingInfoTypePort),
		},
		{
			Name: "REDIS_ENDPOINT",
			Value: ToServiceBindingEnvValue(
				ServiceTypeDbRedis, ServiceBindingInfoTypeEndpoint),
		},
		{
			Name: "REDIS_URL",
			Value: ToServiceBindingEnvValue(
				ServiceTypeDbRedis, ServiceBindingInfoTypeUrl),
		},
		{
			Name: "REDIS_PASSWORD",
			Value: ToServiceBindingEnvValue(
				ServiceTypeDbRedis, ServiceBindingInfoTypePassword),
		},
		{
			Name: "spring.data.redis.url",
			Value: ToServiceBindingEnvValue(
				ServiceTypeDbRedis, ServiceBindingInfoTypeUrl),
		},
	}
}

func GetServiceBindingEnvsForServiceBus(serviceBus AzureDepServiceBus) ([]Env, error) {
	if serviceBus.IsJms {
		switch serviceBus.AuthType {
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []Env{
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
					Value: PlaceHolderForServiceIdentityClientId(),
				},
				{
					Name: "spring.jms.servicebus.namespace",
					Value: ToServiceBindingEnvValue(
						ServiceTypeMessagingServiceBus, ServiceBindingInfoTypeNamespace),
				},
				{
					Name:  "spring.jms.servicebus.connection-string",
					Value: "",
				},
			}, nil
		case internal.AuthTypeConnectionString:
			return []Env{
				{
					Name:  "spring.jms.servicebus.pricing-tier",
					Value: "premium",
				},
				{
					Name: "spring.jms.servicebus.connection-string",
					Value: ToServiceBindingEnvValue(
						ServiceTypeMessagingServiceBus, ServiceBindingInfoTypeConnectionString),
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
			return []Env{}, unsupportedAuthTypeError(ServiceTypeMessagingServiceBus, serviceBus.AuthType)
		}
	} else {
		// service bus, not jms
		switch serviceBus.AuthType {
		case internal.AuthTypeUserAssignedManagedIdentity:
			return []Env{
				// Not add this: spring.cloud.azure.servicebus.connection-string = ""
				// because of this: https://github.com/Azure/azure-sdk-for-java/issues/42880
				{
					Name:  "spring.cloud.azure.servicebus.credential.managed-identity-enabled",
					Value: "true",
				},
				{
					Name:  "spring.cloud.azure.servicebus.credential.client-id",
					Value: PlaceHolderForServiceIdentityClientId(),
				},
				{
					Name: "spring.cloud.azure.servicebus.namespace",
					Value: ToServiceBindingEnvValue(
						ServiceTypeMessagingServiceBus, ServiceBindingInfoTypeNamespace),
				},
			}, nil
		case internal.AuthTypeConnectionString:
			return []Env{
				{
					Name: "spring.cloud.azure.servicebus.namespace",
					Value: ToServiceBindingEnvValue(
						ServiceTypeMessagingServiceBus, ServiceBindingInfoTypeNamespace),
				},
				{
					Name: "spring.cloud.azure.servicebus.connection-string",
					Value: ToServiceBindingEnvValue(
						ServiceTypeMessagingServiceBus, ServiceBindingInfoTypeConnectionString),
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
			return []Env{}, unsupportedAuthTypeError(ServiceTypeMessagingServiceBus, serviceBus.AuthType)
		}
	}
}

func GetServiceBindingEnvsForEventHubsKafka(eventHubs AzureDepEventHubs) ([]Env, error) {
	var springBootVersionDecidedInformation []Env
	if strings.HasPrefix(eventHubs.SpringBootVersion, "2.") {
		springBootVersionDecidedInformation = []Env{
			{
				Name:  "spring.cloud.stream.binders.kafka.environment.spring.main.sources",
				Value: "com.azure.spring.cloud.autoconfigure.eventhubs.kafka.AzureEventHubsKafkaAutoConfiguration",
			},
		}
	} else {
		springBootVersionDecidedInformation = []Env{
			{
				Name: "spring.cloud.stream.binders.kafka.environment.spring.main.sources",
				Value: "com.azure.spring.cloud.autoconfigure.implementation.eventhubs.kafka" +
					".AzureEventHubsKafkaAutoConfiguration",
			},
		}
	}
	var commonInformation []Env
	switch eventHubs.AuthType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		commonInformation = []Env{
			// Not add this: spring.cloud.azure.eventhubs.connection-string = ""
			// because of this: https://github.com/Azure/azure-sdk-for-java/issues/42880
			{
				Name:  "spring.cloud.stream.kafka.binder.brokers",
				Value: ToServiceBindingEnvValue(ServiceTypeMessagingEventHubs, ServiceBindingInfoTypeEndpoint),
			},
			{
				Name:  "spring.cloud.azure.eventhubs.credential.managed-identity-enabled",
				Value: "true",
			},
			{
				Name:  "spring.cloud.azure.eventhubs.credential.client-id",
				Value: PlaceHolderForServiceIdentityClientId(),
			},
		}
	case internal.AuthTypeConnectionString:
		commonInformation = []Env{
			{
				Name:  "spring.cloud.stream.kafka.binder.brokers",
				Value: ToServiceBindingEnvValue(ServiceTypeMessagingEventHubs, ServiceBindingInfoTypeEndpoint),
			},
			{
				Name:  "spring.cloud.azure.eventhubs.connection-string",
				Value: ToServiceBindingEnvValue(ServiceTypeMessagingEventHubs, ServiceBindingInfoTypeConnectionString),
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
		return []Env{}, unsupportedAuthTypeError(ServiceTypeMessagingEventHubs, eventHubs.AuthType)
	}
	return mergeEnvWithDuplicationCheck(springBootVersionDecidedInformation, commonInformation)
}

func GetServiceBindingEnvsForEventHubs(eventHubs AzureDepEventHubs) ([]Env, error) {
	if eventHubs.UseKafka {
		return GetServiceBindingEnvsForEventHubsKafka(eventHubs)
	}
	switch eventHubs.AuthType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		return []Env{
			// Not add this: spring.cloud.azure.eventhubs.connection-string = ""
			// because of this: https://github.com/Azure/azure-sdk-for-java/issues/42880
			{
				Name:  "spring.cloud.azure.eventhubs.credential.managed-identity-enabled",
				Value: "true",
			},
			{
				Name:  "spring.cloud.azure.eventhubs.credential.client-id",
				Value: PlaceHolderForServiceIdentityClientId(),
			},
			{
				Name:  "spring.cloud.azure.eventhubs.namespace",
				Value: ToServiceBindingEnvValue(ServiceTypeMessagingEventHubs, ServiceBindingInfoTypeNamespace),
			},
		}, nil
	case internal.AuthTypeConnectionString:
		return []Env{
			{
				Name:  "spring.cloud.azure.eventhubs.namespace",
				Value: ToServiceBindingEnvValue(ServiceTypeMessagingEventHubs, ServiceBindingInfoTypeNamespace),
			},
			{
				Name:  "spring.cloud.azure.eventhubs.connection-string",
				Value: ToServiceBindingEnvValue(ServiceTypeMessagingEventHubs, ServiceBindingInfoTypeConnectionString),
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
		return []Env{}, unsupportedAuthTypeError(ServiceTypeMessagingEventHubs, eventHubs.AuthType)
	}
}

func GetServiceBindingEnvsForStorageAccount(account AzureDepStorageAccount) ([]Env, error) {
	switch account.AuthType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		return []Env{
			{
				Name: "spring.cloud.azure.eventhubs.processor.checkpoint-store.account-name",
				Value: ToServiceBindingEnvValue(
					ServiceTypeStorage, ServiceBindingInfoTypeAccountName),
			},
			{
				Name:  "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.managed-identity-enabled",
				Value: "true",
			},
			{
				Name:  "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.client-id",
				Value: PlaceHolderForServiceIdentityClientId(),
			},
			{
				Name:  "spring.cloud.azure.eventhubs.processor.checkpoint-store.connection-string",
				Value: "",
			},
		}, nil
	case internal.AuthTypeConnectionString:
		return []Env{
			{
				Name: "spring.cloud.azure.eventhubs.processor.checkpoint-store.account-name",
				Value: ToServiceBindingEnvValue(
					ServiceTypeStorage, ServiceBindingInfoTypeAccountName),
			},
			{
				Name: "spring.cloud.azure.eventhubs.processor.checkpoint-store.connection-string",
				Value: ToServiceBindingEnvValue(
					ServiceTypeStorage, ServiceBindingInfoTypeConnectionString),
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
		return []Env{}, unsupportedAuthTypeError(ServiceTypeStorage, account.AuthType)
	}
}

func GetServiceBindingEnvsForAIModel() []Env {
	return []Env{
		{
			Name:  "AZURE_OPENAI_ENDPOINT",
			Value: ToServiceBindingEnvValue(ServiceTypeOpenAiModel, ServiceBindingInfoTypeEndpoint),
		},
	}
}

func GetServiceBindingEnvsForEurekaServer(eurekaServerName string) []Env {
	return []Env{
		{
			Name:  "eureka.client.register-with-eureka",
			Value: "true",
		},
		{
			Name:  "eureka.client.fetch-registry",
			Value: "true",
		},
		{
			Name:  "eureka.instance.prefer-ip-address",
			Value: "true",
		},
		{
			Name:  "eureka.client.serviceUrl.defaultZone",
			Value: fmt.Sprintf("%s/eureka", GetContainerAppHost(eurekaServerName)),
		},
	}
}

func GetServiceBindingEnvsForConfigServer(configServerName string) []Env {
	return []Env{
		{
			Name: "spring.config.import",
			Value: fmt.Sprintf("optional:configserver:%s?fail-fast=true",
				GetContainerAppHost(configServerName)),
		},
	}
}

func unsupportedAuthTypeError(serviceType ServiceType, authType internal.AuthType) error {
	return fmt.Errorf("unsupported auth type, serviceType = %s, authType = %s", serviceType, authType)
}

func mergeEnvWithDuplicationCheck(a []Env, b []Env) ([]Env, error) {
	ab := append(a, b...)
	var result []Env
	seenName := make(map[string]Env)
	for _, value := range ab {
		if existingValue, exist := seenName[value.Name]; exist {
			if value != existingValue {
				return []Env{}, duplicatedEnvError(existingValue, value)
			}
		} else {
			seenName[value.Name] = value
			result = append(result, value)
		}
	}
	return result, nil
}

func duplicatedEnvError(existingValue Env, newValue Env) error {
	return fmt.Errorf(
		"duplicated environment variable. existingValue = %s, newValue = %s",
		existingValue, newValue,
	)
}
