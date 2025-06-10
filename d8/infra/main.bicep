targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the environment that can be used as part of naming resource convention')
param environmentName string

@minLength(1)
@description('Primary location for all resources')
param location string

@description('Id of the user or app to assign application roles')
param principalId string

// Tags that should be applied to all resources.
// 
// Note that 'azd-service-name' tags should be applied separately to service host resources.
// Example usage:
//   tags: union(tags, { 'azd-service-name': <service name in azure.yaml> })
var tags = {
  'azd-env-name': environmentName
}

param llamaIndexJavascriptExists bool
param isContinuousIntegration bool // Set in main.parameters.json

var llamaIndexConfig = {
  chat: {
    model: 'gpt-4o-mini'
    deployment: 'gpt-4o-mini'
    version: '2024-07-18'
    capacity: 10
  }
  embedding: {
    model: 'text-embedding-3-large'
    deployment: 'text-embedding-3-large'
    version: '1'
    dim: '1024'
    capacity: 10
  }
  model_provider: 'openai'
  openai_api_key: ''
  llm_temperature: '0.7'
  llm_max_tokens: '100'
  openai_api_version: '2024-02-15-preview'
  top_k: '3'
  fileserver_url_prefix: 'http://localhost/api/files'
  system_prompt: 'You are a helpful assistant who helps users with their questions.'
}

// Organize resources in a resource group
resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: 'rg-${environmentName}'
  location: location
  tags: tags
}

module resources 'resources.bicep' = {
  scope: rg
  name: 'resources'
  params: {
    location: location
    tags: tags
    principalId: principalId
    llamaIndexJavascriptExists: llamaIndexJavascriptExists
    llamaIndexConfig: llamaIndexConfig
    isContinuousIntegration: isContinuousIntegration
  }
}
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = resources.outputs.AZURE_CONTAINER_REGISTRY_ENDPOINT
output AZURE_RESOURCE_LLAMA_INDEX_JAVASCRIPT_ID string = resources.outputs.AZURE_RESOURCE_LLAMA_INDEX_JAVASCRIPT_ID

output AZURE_OPENAI_ENDPOINT string = resources.outputs.AZURE_OPENAI_ENDPOINT
output AZURE_DEPLOYMENT_NAME string = llamaIndexConfig.chat.deployment
output AZURE_OPENAI_API_VERSION string = llamaIndexConfig.openai_api_version

//  LlamaIndex configuration
output MODEL_PROVIDER string = llamaIndexConfig.model_provider
output MODEL string = llamaIndexConfig.chat.model
output EMBEDDING_MODEL string = llamaIndexConfig.embedding.model
output EMBEDDING_DIM string = llamaIndexConfig.embedding.dim
output OPENAI_API_KEY string = llamaIndexConfig.openai_api_key
output LLM_TEMPERATURE string = llamaIndexConfig.llm_temperature
output LLM_MAX_TOKENS string = llamaIndexConfig.llm_max_tokens
output TOP_K string = llamaIndexConfig.top_k
output FILESERVER_URL_PREFIX string = llamaIndexConfig.fileserver_url_prefix
output SYSTEM_PROMPT string = llamaIndexConfig.system_prompt
output OPENAI_API_TYPE string = 'AzureOpenAI'
output STORAGE_CACHE_DIR string = './cache'
