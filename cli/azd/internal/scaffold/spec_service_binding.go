package scaffold

import (
	"fmt"
	"strings"
)

// todo merge ServiceType and project.ResourceType
// Not use project.ResourceType because it will cause cycle import.
// Not merge it in current PR to avoid conflict with upstream main branch.
// Solution proposal: define a ServiceType in lower level that can be used both in scaffold and project package.

type ServiceType string

const (
	ServiceTypeDbRedis          ServiceType = "db.redis"
	ServiceTypeDbPostgres       ServiceType = "db.postgres"
	ServiceTypeDbMongo          ServiceType = "db.mongo"
	ServiceTypeHostContainerApp ServiceType = "host.containerapp"
	ServiceTypeOpenAiModel      ServiceType = "ai.openai.model"
)

type ServiceBindingInfoType string

const (
	ServiceBindingInfoTypeHost             ServiceBindingInfoType = "host"
	ServiceBindingInfoTypePort             ServiceBindingInfoType = "port"
	ServiceBindingInfoTypeEndpoint         ServiceBindingInfoType = "endpoint"
	ServiceBindingInfoTypeDatabaseName     ServiceBindingInfoType = "databaseName"
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

func BindToPostgres(serviceSpec *ServiceSpec, postgres *DatabasePostgres) error {
	serviceSpec.DbPostgres = postgres
	envs, err := GetServiceBindingEnvsForPostgres()
	if err != nil {
		return err
	}
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

func GetServiceBindingEnvsForPostgres() ([]Env, error) {
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

func GetServiceBindingEnvsForAIModel() []Env {
	return []Env{
		{
			Name:  "AZURE_OPENAI_ENDPOINT",
			Value: ToServiceBindingEnvValue(ServiceTypeOpenAiModel, ServiceBindingInfoTypeEndpoint),
		},
	}
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
