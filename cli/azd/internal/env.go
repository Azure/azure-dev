package internal

// todo: keep single source for env name in go lang code and resources.bicept

const EnvNamePostgresHost = "POSTGRES_HOST"
const EnvNamePostgresPort = "POSTGRES_PORT"
const EnvNamePostgresUrl = "POSTGRES_URL"
const EnvNamePostgresDatabase = "POSTGRES_DATABASE"
const EnvNamePostgresUsername = "POSTGRES_USERNAME"

// nolint:gosec
const EnvNamePostgresPassword = "POSTGRES_PASSWORD"

const EnvNameRedisHost = "REDIS_HOST"
const EnvNameRedisPort = "REDIS_PORT"
const EnvNameRedisEndpoint = "REDIS_ENDPOINT"
const EnvNameRedisPassword = "REDIS_PASSWORD"
const EnvNameRedisUrl = "REDIS_URL"

const EnvNameMongoDbUrl = "MONGODB_URL"

const EnvNameAzureOpenAiUrl = "AZURE_OPENAI_ENDPOINT"

func ToEnvPlaceHolder(envName string) string {
	return "${" + envName + "}"
}
