package binding

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
)

func GetBindingEnvs(source Source, target Target) (map[string]string,
	error) {
	switch source.Type {
	case Java, SpringBoot: // todo: support other Java types
		return GetBindingEnvsForSpringBoot(source, target)
	default:
		return GetBindingEnvsForCommonSource(target)
	}
}

type Source struct {
	Type     SourceType
	Metadata map[MetadataType]string
}

type SourceType string
type MetadataType string

const (
	Java       SourceType = "java"
	SpringBoot SourceType = "springBoot"
	Unknown    SourceType = "unknown"
)

type Target struct {
	Type     TargetType
	Name     string
	AuthType internal.AuthType
}

type TargetType string

const (
	AzureDatabaseForPostgresql TargetType = "azure.db.postgresql"
	AzureDatabaseForMysql      TargetType = "azure.db.mysql"
	AzureCacheForRedis         TargetType = "azure.db.redis"
	AzureCosmosDBForMongoDB    TargetType = "azure.db.cosmos.mongo"
	AzureCosmosDBForNoSQL      TargetType = "azure.db.cosmos.nosql"
	AzureContainerApp          TargetType = "azure.host.containerapp"
	AzureOpenAiModel           TargetType = "azure.ai.openai.model"
	AzureServiceBus            TargetType = "azure.messaging.servicebus"
	AzureEventHubs             TargetType = "azure.messaging.eventhubs"
	AzureStorageAccount        TargetType = "azure.storage"
)

type InfoType string

const (
	InfoTypeHost                                      InfoType = "host"
	InfoTypePort                                      InfoType = "port"
	InfoTypeEndpoint                                  InfoType = "endpoint"
	InfoTypeDatabaseName                              InfoType = "databaseName"
	InfoTypeNamespace                                 InfoType = "namespace"
	InfoTypeAccountName                               InfoType = "accountName"
	InfoTypeUsername                                  InfoType = "username"
	InfoTypePassword                                  InfoType = "password"
	InfoTypeUrl                                       InfoType = "url"
	InfoTypeJdbcUrl                                   InfoType = "jdbcUrl"
	InfoTypeConnectionString                          InfoType = "connectionString"
	InfoTypeSourceUserAssignedManagedIdentityClientId InfoType = "sourceUserAssignedManagedIdentityClientId"
)

const bindingEnvPrefix = "${binding:"
const bindingEnvSuffix = "}"
const bindingEnvFormat = bindingEnvPrefix + "%s:%s:%s" + bindingEnvSuffix
const SourceUserAssignedManagedIdentityClientId = bindingEnvPrefix +
	"*:*:" + string(InfoTypeSourceUserAssignedManagedIdentityClientId) + bindingEnvSuffix

func IsBindingEnv(value string) bool {
	_, infoType := ToTargetAndInfoType(value)
	return infoType != ""
}

func ToBindingEnv(target Target, infoType InfoType) string {
	return fmt.Sprintf(bindingEnvFormat, target.Type, target.Name, infoType)
}

func ReplaceBindingEnv(value string, substr string) string {
	prefixIndex := strings.Index(value, bindingEnvPrefix)
	if prefixIndex == -1 {
		return value
	}
	suffixIndex := strings.Index(value, bindingEnvSuffix)
	if suffixIndex == -1 {
		return value
	}
	if prefixIndex >= suffixIndex {
		return value
	}
	return value[0:prefixIndex] + substr + value[suffixIndex+1:]
}

func ToTargetAndInfoType(value string) (target Target, infoType InfoType) {
	prefixIndex := strings.Index(value, bindingEnvPrefix)
	if prefixIndex == -1 {
		return Target{}, ""
	}
	suffixIndex := strings.Index(value, bindingEnvSuffix)
	if suffixIndex == -1 {
		return Target{}, ""
	}
	if prefixIndex >= suffixIndex {
		return Target{}, ""
	}
	bindingEnv := value[prefixIndex:suffixIndex]
	a := strings.Split(bindingEnv, ":")
	if len(a) != 4 {
		return Target{}, ""
	}
	targetTypeString := a[1]
	targetNameString := a[2]
	infoTypeString := a[3]
	return Target{Type: TargetType(targetTypeString), Name: targetNameString}, InfoType(infoTypeString)
}

func MergeMapWithDuplicationCheck(a map[string]string, b map[string]string) (map[string]string, error) {
	result := make(map[string]string)
	for k, v := range a {
		result[k] = v
	}
	for key, value := range b {
		if existingValue, exist := result[key]; exist {
			if value != existingValue {
				return nil, duplicatedEnvError(existingValue, value)
			}
		} else {
			result[key] = value
		}
	}
	return result, nil
}

func duplicatedEnvError(existingValue string, newValue string) error {
	return fmt.Errorf(
		"duplicated environment variable. existingValue = %s, newValue = %s",
		existingValue, newValue,
	)
}
