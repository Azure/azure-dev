package scaffold

import (
	"strconv"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/binding"
)

func BindToPostgres(sourceType binding.SourceType, serviceSpec *ServiceSpec, postgres *DatabasePostgres) error {
	serviceSpec.DbPostgres = postgres
	return addBindingEnvs(serviceSpec,
		binding.Source{Type: sourceType},
		binding.Target{Type: binding.AzureDatabaseForPostgresql, AuthType: postgres.AuthType})
}

func BindToMySql(sourceType binding.SourceType, serviceSpec *ServiceSpec, mysql *DatabaseMySql) error {
	serviceSpec.DbMySql = mysql
	return addBindingEnvs(serviceSpec,
		binding.Source{Type: sourceType},
		binding.Target{Type: binding.AzureDatabaseForMysql, AuthType: mysql.AuthType})
}

func BindToMongoDb(sourceType binding.SourceType, serviceSpec *ServiceSpec, mongo *DatabaseCosmosMongo) error {
	serviceSpec.DbCosmosMongo = mongo
	return addBindingEnvs(serviceSpec,
		binding.Source{Type: sourceType},
		binding.Target{Type: binding.AzureCosmosDBForMongoDB, AuthType: internal.AuthTypeConnectionString})
}

func BindToCosmosDb(sourceType binding.SourceType, serviceSpec *ServiceSpec, cosmos *DatabaseCosmosAccount) error {
	serviceSpec.DbCosmos = cosmos
	return addBindingEnvs(serviceSpec,
		binding.Source{Type: sourceType},
		binding.Target{Type: binding.AzureCosmosDBForNoSQL, AuthType: internal.AuthTypeUserAssignedManagedIdentity})
}

func BindToRedis(sourceType binding.SourceType, serviceSpec *ServiceSpec, redis *DatabaseRedis) error {
	serviceSpec.DbRedis = redis
	return addBindingEnvs(serviceSpec,
		binding.Source{Type: sourceType},
		binding.Target{Type: binding.AzureCacheForRedis, AuthType: internal.AuthTypePassword})
}

func BindToServiceBus(sourceType binding.SourceType, serviceSpec *ServiceSpec, serviceBus *AzureDepServiceBus) error {
	serviceSpec.AzureServiceBus = serviceBus
	return addBindingEnvs(serviceSpec,
		binding.Source{
			Type:     sourceType,
			Metadata: map[binding.MetadataType]string{binding.IsSpringBootJms: strconv.FormatBool(serviceBus.IsJms)}},
		binding.Target{Type: binding.AzureServiceBus, AuthType: serviceBus.AuthType})
}

func BindToEventHubs(sourceType binding.SourceType, serviceSpec *ServiceSpec, eventHubs *AzureDepEventHubs) error {
	serviceSpec.AzureEventHubs = eventHubs
	return addBindingEnvs(serviceSpec,
		binding.Source{
			Type: sourceType,
			Metadata: map[binding.MetadataType]string{
				binding.IsSpringBootKafka: strconv.FormatBool(eventHubs.UseKafka),
				binding.SpringBootVersion: eventHubs.SpringBootVersion}},
		binding.Target{Type: binding.AzureEventHubs, AuthType: eventHubs.AuthType})
}

func BindToStorageAccount(sourceType binding.SourceType, serviceSpec *ServiceSpec,
	account *AzureDepStorageAccount) error {
	serviceSpec.AzureStorageAccount = account
	return addBindingEnvs(serviceSpec,
		binding.Source{Type: sourceType},
		binding.Target{Type: binding.AzureStorageAccount, AuthType: account.AuthType})
}

func BindToAIModels(sourceType binding.SourceType, serviceSpec *ServiceSpec, model string) error {
	serviceSpec.AIModels = append(serviceSpec.AIModels, AIModelReference{Name: model})
	return addBindingEnvs(serviceSpec, binding.Source{Type: sourceType},
		binding.Target{Type: binding.AzureOpenAiModel, AuthType: internal.AuthTypeUnspecified})
}

func addBindingEnvs(serviceSpec *ServiceSpec, source binding.Source, target binding.Target) error {
	envs, err := binding.GetBindingEnvs(source, target)
	if err != nil {
		return err
	}
	serviceSpec.Envs, err = binding.MergeMapWithDuplicationCheck(serviceSpec.Envs, envs)
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
	b.Backend.Frontends = append(b.Backend.Frontends, ServiceReference{Name: a.Name})
}
