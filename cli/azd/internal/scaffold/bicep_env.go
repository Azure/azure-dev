package scaffold

import (
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal"
	"strings"
)

func ToBicepEnv(env Env) BicepEnv {
	if isResourceConnectionEnv(env.Value) {
		resourceType, resourceInfoType := toResourceConnectionInfo(env.Value)
		value, ok := bicepEnv[resourceType][resourceInfoType]
		if !ok {
			panic(unsupportedType(env))
		}
		if isSecret(resourceInfoType) {
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
			name == "spring.datasource.azure.passwordless-enabled"
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
var bicepEnv = map[ResourceType]map[ResourceInfoType]string{
	ResourceTypeDbPostgres: {
		ResourceInfoTypeHost:         "postgreServer.outputs.fqdn",
		ResourceInfoTypePort:         "'5432'",
		ResourceInfoTypeDatabaseName: "postgreSqlDatabaseName",
		ResourceInfoTypeUsername:     "postgreSqlDatabaseUser",
		ResourceInfoTypePassword:     "postgreSqlDatabasePassword",
		ResourceInfoTypeUrl: "'postgresql://${postgreSqlDatabaseUser}:${postgreSqlDatabasePassword}@" +
			"${postgreServer.outputs.fqdn}:5432/${postgreSqlDatabaseName}'",
		ResourceInfoTypeJdbcUrl: "'jdbc:postgresql://${postgreServer.outputs.fqdn}:5432/${postgreSqlDatabaseName}'",
	},
	ResourceTypeDbMySQL: {
		ResourceInfoTypeHost:         "mysqlServer.outputs.fqdn",
		ResourceInfoTypePort:         "'3306'",
		ResourceInfoTypeDatabaseName: "mysqlDatabaseName",
		ResourceInfoTypeUsername:     "mysqlDatabaseUser",
		ResourceInfoTypePassword:     "mysqlDatabasePassword",
		ResourceInfoTypeUrl: "'mysql://${mysqlDatabaseUser}:${mysqlDatabasePassword}@" +
			"${mysqlServer.outputs.fqdn}:3306/${mysqlDatabaseName}'",
		ResourceInfoTypeJdbcUrl: "'jdbc:mysql://${mysqlServer.outputs.fqdn}:3306/${mysqlDatabaseName}'",
	},
	ResourceTypeDbRedis: {
		ResourceInfoTypeHost:     "redis.outputs.hostName",
		ResourceInfoTypePort:     "string(redis.outputs.sslPort)",
		ResourceInfoTypeEndpoint: "'${redis.outputs.hostName}:${redis.outputs.sslPort}'",
		ResourceInfoTypePassword: wrapToKeyVaultSecretValue("redisConn.outputs.keyVaultUrlForPass"),
		ResourceInfoTypeUrl:      wrapToKeyVaultSecretValue("redisConn.outputs.keyVaultUrlForUrl"),
	},
	ResourceTypeDbMongo: {
		ResourceInfoTypeDatabaseName: "mongoDatabaseName",
		ResourceInfoTypeUrl:          wrapToKeyVaultSecretValue("cosmos.outputs.exportedSecrets['MONGODB-URL'].secretUri"),
	},
	ResourceTypeDbCosmos: {
		ResourceInfoTypeEndpoint:     "cosmos.outputs.endpoint",
		ResourceInfoTypeDatabaseName: "cosmosDatabaseName",
	},
	ResourceTypeMessagingServiceBus: {
		ResourceInfoTypeNamespace:        "serviceBusNamespace.outputs.name",
		ResourceInfoTypeConnectionString: wrapToKeyVaultSecretValue("serviceBusConnectionString.outputs.keyVaultUrl"),
	},
	ResourceTypeMessagingEventHubs: {
		ResourceInfoTypeNamespace:        "eventHubNamespace.outputs.name",
		ResourceInfoTypeConnectionString: wrapToKeyVaultSecretValue("eventHubsConnectionString.outputs.keyVaultUrl"),
	},
	ResourceTypeMessagingKafka: {
		ResourceInfoTypeEndpoint:         "'${eventHubNamespace.outputs.name}.servicebus.windows.net:9093'",
		ResourceInfoTypeConnectionString: wrapToKeyVaultSecretValue("eventHubsConnectionString.outputs.keyVaultUrl"),
	},
	ResourceTypeStorage: {
		ResourceInfoTypeAccountName:      "storageAccountName",
		ResourceInfoTypeConnectionString: wrapToKeyVaultSecretValue("storageAccountConnectionString.outputs.keyVaultUrl"),
	},
	ResourceTypeOpenAiModel: {
		ResourceInfoTypeEndpoint: "account.outputs.endpoint",
	},
	ResourceTypeHostContainerApp: {},
}

func unsupportedType(env Env) string {
	return fmt.Sprintf("unsupported connection info type for resource type. "+
		"value = %s", env.Value)
}

func PlaceHolderForServiceIdentityClientId() string {
	return "__PlaceHolderForServiceIdentityClientId"
}

func isSecret(info ResourceInfoType) bool {
	return info == ResourceInfoTypePassword || info == ResourceInfoTypeUrl || info == ResourceInfoTypeConnectionString
}

func secretName(env Env) string {
	resourceType, resourceInfoType := toResourceConnectionInfo(env.Value)
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
