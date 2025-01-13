package scaffold

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
)

func ToBicepEnv(env Env) BicepEnv {
	if isServiceBindingEnvValue(env.Value) {
		serviceType, infoType := toServiceTypeAndServiceBindingInfoType(env.Value)
		value, ok := bicepEnv[serviceType][infoType]
		if !ok {
			panic(unsupportedType(env))
		}
		if isSecret(infoType) {
			if isKeyVaultSecret(value) {
				return BicepEnv{
					BicepEnvType: BicepEnvTypeKeyVaultSecret,
					Name:         env.Name,
					SecretName:   secretName(env),
					SecretValue:  unwrapKeyVaultSecretValue(value),
				}
			} else {
				return BicepEnv{
					BicepEnvType: BicepEnvTypeSecret,
					Name:         env.Name,
					SecretName:   secretName(env),
					SecretValue:  value,
				}
			}
		} else {
			return BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           env.Name,
				PlainTextValue: value,
			}
		}
	} else {
		return BicepEnv{
			BicepEnvType:   BicepEnvTypePlainText,
			Name:           env.Name,
			PlainTextValue: toBicepEnvPlainTextValue(env.Value),
		}
	}
}

func ShouldAddToBicepFile(spec ServiceSpec, name string) bool {
	return !willBeAddedByServiceConnector(spec, name)
}

func willBeAddedByServiceConnector(spec ServiceSpec, name string) bool {
	if (spec.DbPostgres != nil && spec.DbPostgres.AuthType == internal.AuthTypeUserAssignedManagedIdentity) ||
		(spec.DbMySql != nil && spec.DbMySql.AuthType == internal.AuthTypeUserAssignedManagedIdentity) {
		return name == "spring.datasource.url" ||
			name == "spring.datasource.username" ||
			name == "spring.datasource.azure.passwordless-enabled" ||
			name == "spring.cloud.azure.credential.client-id" ||
			name == "spring.cloud.azure.credential.managed-identity-enabled"
	} else {
		return false
	}
}

// inputStringExample -> 'inputStringExample'
func addQuotation(input string) string {
	return fmt.Sprintf("'%s'", input)
}

// 'inputStringExample' -> 'inputStringExample'
// '${inputSingleVariableExample}' -> inputSingleVariableExample
// '${HOST}:${PORT}' -> '${HOST}:${PORT}'
func removeQuotationIfItIsASingleVariable(input string) string {
	prefix := "'${"
	suffix := "}'"
	if strings.HasPrefix(input, prefix) && strings.HasSuffix(input, suffix) {
		prefixTrimmed := strings.TrimPrefix(input, prefix)
		trimmed := strings.TrimSuffix(prefixTrimmed, suffix)
		if !strings.ContainsAny(trimmed, "}") {
			return trimmed
		} else {
			return input
		}
	} else {
		return input
	}
}

// The BicepEnv.PlainTextValue is handled as variable by default.
// If the value is string, it should contain (').
// Here are some examples of input and output:
// inputStringExample -> 'inputStringExample'
// ${inputSingleVariableExample} -> inputSingleVariableExample
// ${HOST}:${PORT} -> '${HOST}:${PORT}'
func toBicepEnvPlainTextValue(input string) string {
	return removeQuotationIfItIsASingleVariable(addQuotation(input))
}

// BicepEnv
//
// For Name and SecretName, they are handled as string by default.
// Which means quotation will be added before they are used in bicep file, because they are always string value.
//
// For PlainTextValue and SecretValue, they are handled as variable by default.
// When they are string value, quotation should be contained by themselves.
// Set variable as default is mainly to avoid this problem:
// https://learn.microsoft.com/en-us/azure/azure-resource-manager/bicep/linter-rule-simplify-interpolation
type BicepEnv struct {
	BicepEnvType   BicepEnvType
	Name           string
	PlainTextValue string
	SecretName     string
	SecretValue    string
}

type BicepEnvType string

const (
	BicepEnvTypePlainText      BicepEnvType = "plainText"
	BicepEnvTypeSecret         BicepEnvType = "secret"
	BicepEnvTypeKeyVaultSecret BicepEnvType = "keyVaultSecret"
)

// Note: The value is handled as variable.
// If the value is string, it should contain quotation inside itself.
var bicepEnv = map[ServiceType]map[ServiceBindingInfoType]string{
	ServiceTypeDbPostgres: {
		ServiceBindingInfoTypeHost:         "postgreServer.outputs.fqdn",
		ServiceBindingInfoTypePort:         "'5432'",
		ServiceBindingInfoTypeDatabaseName: "postgreSqlDatabaseName",
		ServiceBindingInfoTypeUsername:     "postgreSqlDatabaseUser",
		ServiceBindingInfoTypePassword:     "postgreSqlDatabasePassword",
		ServiceBindingInfoTypeUrl: "'postgresql://${postgreSqlDatabaseUser}:${postgreSqlDatabasePassword}@" +
			"${postgreServer.outputs.fqdn}:5432/${postgreSqlDatabaseName}'",
		ServiceBindingInfoTypeJdbcUrl: "'jdbc:postgresql://${postgreServer.outputs.fqdn}:5432/" +
			"${postgreSqlDatabaseName}'",
	},
	ServiceTypeDbMySQL: {
		ServiceBindingInfoTypeHost:         "mysqlServer.outputs.fqdn",
		ServiceBindingInfoTypePort:         "'3306'",
		ServiceBindingInfoTypeDatabaseName: "mysqlDatabaseName",
		ServiceBindingInfoTypeUsername:     "mysqlDatabaseUser",
		ServiceBindingInfoTypePassword:     "mysqlDatabasePassword",
		ServiceBindingInfoTypeUrl: "'mysql://${mysqlDatabaseUser}:${mysqlDatabasePassword}@" +
			"${mysqlServer.outputs.fqdn}:3306/${mysqlDatabaseName}'",
		ServiceBindingInfoTypeJdbcUrl: "'jdbc:mysql://${mysqlServer.outputs.fqdn}:3306/${mysqlDatabaseName}'",
	},
	ServiceTypeDbRedis: {
		ServiceBindingInfoTypeHost:     "redis.outputs.hostName",
		ServiceBindingInfoTypePort:     "string(redis.outputs.sslPort)",
		ServiceBindingInfoTypeEndpoint: "'${redis.outputs.hostName}:${redis.outputs.sslPort}'",
		ServiceBindingInfoTypePassword: wrapToKeyVaultSecretValue("redisConn.outputs.keyVaultUrlForPass"),
		ServiceBindingInfoTypeUrl:      wrapToKeyVaultSecretValue("redisConn.outputs.keyVaultUrlForUrl"),
	},
	ServiceTypeDbMongo: {
		ServiceBindingInfoTypeDatabaseName: "mongoDatabaseName",
		ServiceBindingInfoTypeUrl: wrapToKeyVaultSecretValue(
			"cosmos.outputs.exportedSecrets['MONGODB-URL'].secretUri",
		),
	},
	ServiceTypeDbCosmos: {
		ServiceBindingInfoTypeEndpoint:     "cosmos.outputs.endpoint",
		ServiceBindingInfoTypeDatabaseName: "cosmosDatabaseName",
	},
	ServiceTypeMessagingServiceBus: {
		ServiceBindingInfoTypeNamespace: "serviceBusNamespace.outputs.name",
		ServiceBindingInfoTypeConnectionString: wrapToKeyVaultSecretValue(
			"serviceBusConnectionString.outputs.keyVaultUrl",
		),
	},
	ServiceTypeMessagingEventHubs: {
		ServiceBindingInfoTypeNamespace: "eventHubNamespace.outputs.name",
		ServiceBindingInfoTypeEndpoint:  "'${eventHubNamespace.outputs.name}.servicebus.windows.net:9093'",
		ServiceBindingInfoTypeConnectionString: wrapToKeyVaultSecretValue(
			"eventHubsConnectionString.outputs.keyVaultUrl",
		),
	},
	ServiceTypeStorage: {
		ServiceBindingInfoTypeAccountName: "storageAccountName",
		ServiceBindingInfoTypeConnectionString: wrapToKeyVaultSecretValue(
			"storageAccountConnectionString.outputs.keyVaultUrl",
		),
	},
	ServiceTypeOpenAiModel: {
		ServiceBindingInfoTypeEndpoint: "account.outputs.endpoint",
	},
	ServiceTypeHostContainerApp: {
		ServiceBindingInfoTypeHost: "https://{{BackendName}}.${containerAppsEnvironment.outputs.defaultDomain}",
	},
}

func GetContainerAppHost(name string) string {
	return strings.ReplaceAll(
		bicepEnv[ServiceTypeHostContainerApp][ServiceBindingInfoTypeHost],
		"{{BackendName}}",
		name,
	)
}

func unsupportedType(env Env) string {
	return fmt.Sprintf(
		"unsupported connection info type for resource type. value = %s", env.Value,
	)
}

func PlaceHolderForServiceIdentityClientId() string {
	return "__PlaceHolderForServiceIdentityClientId"
}

func isSecret(info ServiceBindingInfoType) bool {
	return info == ServiceBindingInfoTypePassword || info == ServiceBindingInfoTypeUrl ||
		info == ServiceBindingInfoTypeConnectionString
}

func secretName(env Env) string {
	resourceType, resourceInfoType := toServiceTypeAndServiceBindingInfoType(env.Value)
	name := fmt.Sprintf("%s-%s", resourceType, resourceInfoType)
	lowerCaseName := strings.ToLower(name)
	noDotName := strings.Replace(lowerCaseName, ".", "-", -1)
	noUnderscoreName := strings.Replace(noDotName, "_", "-", -1)
	return noUnderscoreName
}

var keyVaultSecretPrefix = "keyvault:"

func isKeyVaultSecret(value string) bool {
	return strings.HasPrefix(value, keyVaultSecretPrefix)
}

func wrapToKeyVaultSecretValue(value string) string {
	return fmt.Sprintf("%s%s", keyVaultSecretPrefix, value)
}

func unwrapKeyVaultSecretValue(value string) string {
	return strings.TrimPrefix(value, keyVaultSecretPrefix)
}
