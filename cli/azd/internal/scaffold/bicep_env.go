package scaffold

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/binding"
)

const placeholderOfSourceClientId = "__PlaceHolder__SourceClientId"

func ToBicepEnv(name string, value string) BicepEnv {
	if binding.IsBindingEnv(value) {
		if value == binding.SourceUserAssignedManagedIdentityClientId {
			return BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           name,
				PlainTextValue: placeholderOfSourceClientId,
			}
		}
		target, infoType := binding.ToTargetAndInfoType(value)
		bicepEnvValue, ok := bicepEnv[target.Type][infoType]
		if !ok {
			panic(unsupportedType(target.Type, infoType))
		}
		if strings.HasPrefix(bicepEnvValue, "'") && strings.HasSuffix(bicepEnvValue, "'") {
			bicepEnvValue = bicepEnvValue[1 : len(bicepEnvValue)-1]
			bicepEnvValue = "'" + binding.ReplaceBindingEnv(value, bicepEnvValue) + "'"
		} else {
			bicepEnvValue = binding.ReplaceBindingEnv(value, bicepEnvValue)
		}
		if isSecret(infoType) {
			if isKeyVaultSecret(bicepEnvValue) {
				return BicepEnv{
					BicepEnvType: BicepEnvTypeKeyVaultSecret,
					Name:         name,
					SecretName:   secretName(value),
					SecretValue:  unwrapKeyVaultSecretValue(bicepEnvValue),
				}
			} else {
				return BicepEnv{
					BicepEnvType: BicepEnvTypeSecret,
					Name:         name,
					SecretName:   secretName(value),
					SecretValue:  bicepEnvValue,
				}
			}
		} else {
			if target.Type == binding.AzureContainerApp && target.Name != "" {
				bicepEnvValue = strings.ReplaceAll(bicepEnvValue, "{{BackendName}}", BicepName(target.Name))
			}
			return BicepEnv{
				BicepEnvType:   BicepEnvTypePlainText,
				Name:           name,
				PlainTextValue: bicepEnvValue,
			}
		}
	} else {
		return BicepEnv{
			BicepEnvType:   BicepEnvTypePlainText,
			Name:           name,
			PlainTextValue: toBicepEnvPlainTextValue(value),
		}
	}
}

func IsPlaceholderOfSourceClientId(value string) bool {
	return value == placeholderOfSourceClientId
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
var bicepEnv = map[binding.TargetType]map[binding.InfoType]string{
	binding.AzureDatabaseForPostgresql: {
		binding.InfoTypeHost:         "postgreServer.outputs.fqdn",
		binding.InfoTypePort:         "'5432'",
		binding.InfoTypeDatabaseName: "postgreSqlDatabaseName",
		binding.InfoTypeUsername:     "postgreSqlDatabaseUser",
		binding.InfoTypePassword:     "postgreSqlDatabasePassword",
		binding.InfoTypeUrl: "'postgresql://${postgreSqlDatabaseUser}:${postgreSqlDatabasePassword}@" +
			"${postgreServer.outputs.fqdn}:5432/${postgreSqlDatabaseName}'",
		binding.InfoTypeJdbcUrl: "'jdbc:postgresql://${postgreServer.outputs.fqdn}:5432/" +
			"${postgreSqlDatabaseName}'",
	},
	binding.AzureDatabaseForMysql: {
		binding.InfoTypeHost:         "mysqlServer.outputs.fqdn",
		binding.InfoTypePort:         "'3306'",
		binding.InfoTypeDatabaseName: "mysqlDatabaseName",
		binding.InfoTypeUsername:     "mysqlDatabaseUser",
		binding.InfoTypePassword:     "mysqlDatabasePassword",
		binding.InfoTypeUrl: "'mysql://${mysqlDatabaseUser}:${mysqlDatabasePassword}@" +
			"${mysqlServer.outputs.fqdn}:3306/${mysqlDatabaseName}'",
		binding.InfoTypeJdbcUrl: "'jdbc:mysql://${mysqlServer.outputs.fqdn}:3306/${mysqlDatabaseName}'",
	},
	binding.AzureCacheForRedis: {
		binding.InfoTypeHost:     "redis.outputs.hostName",
		binding.InfoTypePort:     "string(redis.outputs.sslPort)",
		binding.InfoTypeEndpoint: "'${redis.outputs.hostName}:${redis.outputs.sslPort}'",
		binding.InfoTypePassword: wrapToKeyVaultSecretValue("redisConn.outputs.keyVaultUrlForPass"),
		binding.InfoTypeUrl:      wrapToKeyVaultSecretValue("redisConn.outputs.keyVaultUrlForUrl"),
	},
	binding.AzureCosmosDBForMongoDB: {
		binding.InfoTypeDatabaseName: "mongoDatabaseName",
		binding.InfoTypeUrl: wrapToKeyVaultSecretValue(
			"cosmos.outputs.exportedSecrets['MONGODB-URL'].secretUri",
		),
	},
	binding.AzureCosmosDBForNoSQL: {
		binding.InfoTypeEndpoint:     "cosmos.outputs.endpoint",
		binding.InfoTypeDatabaseName: "cosmosDatabaseName",
	},
	binding.AzureServiceBus: {
		binding.InfoTypeNamespace: "serviceBusNamespace.outputs.name",
		binding.InfoTypeConnectionString: wrapToKeyVaultSecretValue(
			"serviceBusConnectionString.outputs.keyVaultUrl",
		),
	},
	binding.AzureEventHubs: {
		binding.InfoTypeNamespace: "eventHubNamespace.outputs.name",
		binding.InfoTypeEndpoint:  "'${eventHubNamespace.outputs.name}.servicebus.windows.net:9093'",
		binding.InfoTypeConnectionString: wrapToKeyVaultSecretValue(
			"eventHubsConnectionString.outputs.keyVaultUrl",
		),
	},
	binding.AzureStorageAccount: {
		binding.InfoTypeAccountName: "storageAccountName",
		binding.InfoTypeConnectionString: wrapToKeyVaultSecretValue(
			"storageAccountConnectionString.outputs.keyVaultUrl",
		),
	},
	binding.AzureOpenAiModel: {
		binding.InfoTypeEndpoint: "account.outputs.endpoint",
	},
	binding.AzureContainerApp: {
		binding.InfoTypeHost: "'https://{{BackendName}}.${containerAppsEnvironment.outputs.defaultDomain}'",
	},
}

func unsupportedType(targetType binding.TargetType, infoType binding.InfoType) string {
	return fmt.Sprintf(
		"unsupported connection info type for resource type. targetType = %s, targetType = %s",
		targetType, infoType)
}

func isSecret(info binding.InfoType) bool {
	return info == binding.InfoTypePassword || info == binding.InfoTypeUrl ||
		info == binding.InfoTypeConnectionString
}

func secretName(envValue string) string {
	target, infoType := binding.ToTargetAndInfoType(envValue)
	name := fmt.Sprintf("%s-%s", target.Type, infoType)
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
