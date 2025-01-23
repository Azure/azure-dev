package binding

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
)

func GetBindingEnvsForCommonSource(target Target) (map[string]string, error) {
	switch target.Type {
	case AzureDatabaseForPostgresql:
		return GetBindingEnvsForCommonSourceToPostgresql(target.AuthType)
	case AzureDatabaseForMysql:
		return GetBindingEnvsForCommonSourceToMysql(target.AuthType)
	case AzureCosmosDBForMongoDB:
		return GetBindingEnvsForCommonSourceToMongoDB(target.AuthType)
	case AzureCacheForRedis:
		return GetBindingEnvsForCommonSourceToRedis(target.AuthType)
	case AzureOpenAiModel:
		return GetServiceBindingEnvsForAIModel(target.AuthType)
	default:
		return nil, fmt.Errorf("unsupported target type when binding for spring boot app, target.Type = %s",
			target.Type)
	}
}

func GetBindingEnvsForCommonSourceToPostgresql(authType internal.AuthType) (map[string]string, error) {
	switch authType {
	case internal.AuthTypePassword:
		return map[string]string{
			"POSTGRES_USERNAME": ToBindingEnv(Target{Type: AzureDatabaseForPostgresql}, InfoTypeUsername),
			"POSTGRES_PASSWORD": ToBindingEnv(Target{Type: AzureDatabaseForPostgresql}, InfoTypePassword),
			"POSTGRES_HOST":     ToBindingEnv(Target{Type: AzureDatabaseForPostgresql}, InfoTypeHost),
			"POSTGRES_DATABASE": ToBindingEnv(Target{Type: AzureDatabaseForPostgresql}, InfoTypeDatabaseName),
			"POSTGRES_PORT":     ToBindingEnv(Target{Type: AzureDatabaseForPostgresql}, InfoTypePort),
			"POSTGRES_URL":      ToBindingEnv(Target{Type: AzureDatabaseForPostgresql}, InfoTypeUrl),
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureDatabaseForPostgresql, authType)
	}
}

func GetBindingEnvsForCommonSourceToMysql(authType internal.AuthType) (map[string]string, error) {
	switch authType {
	case internal.AuthTypePassword:
		return map[string]string{
			"MYSQL_USERNAME": ToBindingEnv(Target{Type: AzureDatabaseForMysql}, InfoTypeUsername),
			"MYSQL_PASSWORD": ToBindingEnv(Target{Type: AzureDatabaseForMysql}, InfoTypePassword),
			"MYSQL_HOST":     ToBindingEnv(Target{Type: AzureDatabaseForMysql}, InfoTypeHost),
			"MYSQL_DATABASE": ToBindingEnv(Target{Type: AzureDatabaseForMysql}, InfoTypeDatabaseName),
			"MYSQL_PORT":     ToBindingEnv(Target{Type: AzureDatabaseForMysql}, InfoTypePort),
			"MYSQL_URL":      ToBindingEnv(Target{Type: AzureDatabaseForMysql}, InfoTypeUrl),
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureDatabaseForMysql, authType)
	}
}

func GetBindingEnvsForCommonSourceToMongoDB(authType internal.AuthType) (map[string]string, error) {
	switch authType {
	case internal.AuthTypeConnectionString:
		return map[string]string{
			"MONGODB_URL": ToBindingEnv(Target{Type: AzureCosmosDBForMongoDB}, InfoTypeUrl),
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureCosmosDBForMongoDB, authType)
	}
}

func GetBindingEnvsForCommonSourceToRedis(authType internal.AuthType) (map[string]string, error) {
	switch authType {
	case internal.AuthTypePassword:
		return map[string]string{
			"REDIS_HOST":     ToBindingEnv(Target{Type: AzureCacheForRedis}, InfoTypeHost),
			"REDIS_PORT":     ToBindingEnv(Target{Type: AzureCacheForRedis}, InfoTypePort),
			"REDIS_ENDPOINT": ToBindingEnv(Target{Type: AzureCacheForRedis}, InfoTypeEndpoint),
			"REDIS_URL":      ToBindingEnv(Target{Type: AzureCacheForRedis}, InfoTypeUrl),
			"REDIS_PASSWORD": ToBindingEnv(Target{Type: AzureCacheForRedis}, InfoTypePassword),
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureCacheForRedis, authType)
	}
}

func GetServiceBindingEnvsForAIModel(authType internal.AuthType) (map[string]string, error) {
	switch authType {
	case internal.AuthTypeUserAssignedManagedIdentity:
		return map[string]string{
			"AZURE_OPENAI_ENDPOINT": ToBindingEnv(Target{Type: AzureOpenAiModel}, InfoTypeEndpoint),
		}, nil
	default:
		return nil, unsupportedAuthTypeError(AzureOpenAiModel, authType)
	}
}

func unsupportedAuthTypeError(targetType TargetType, authType internal.AuthType) error {
	return fmt.Errorf("unsupported auth type, serviceType = %s, authType = %s", targetType, authType)
}
