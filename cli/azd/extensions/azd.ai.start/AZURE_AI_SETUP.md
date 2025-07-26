# Azure AI Integration Setup

This AI agent can work with both OpenAI and Azure OpenAI Service. Here's how to configure each:

## Option 1: Azure OpenAI Service (Recommended for Azure users)

Azure OpenAI provides the same models as OpenAI but hosted on Azure infrastructure with enterprise security and compliance.

### Prerequisites
1. Azure subscription
2. Azure OpenAI resource created in Azure portal
3. GPT model deployed (e.g., GPT-3.5-turbo or GPT-4)

### Environment Variables
```bash
# Set these environment variables for Azure OpenAI
export AZURE_OPENAI_ENDPOINT="https://your-resource-name.openai.azure.com"
export AZURE_OPENAI_API_KEY="your-azure-openai-api-key"
export AZURE_OPENAI_DEPLOYMENT_NAME="your-gpt-deployment-name"
```

### PowerShell (Windows)
```powershell
$env:AZURE_OPENAI_ENDPOINT="https://your-resource-name.openai.azure.com"
$env:AZURE_OPENAI_API_KEY="your-azure-openai-api-key"
$env:AZURE_OPENAI_DEPLOYMENT_NAME="your-gpt-deployment-name"
```

## Option 2: OpenAI API (Direct)

### Environment Variables
```bash
export OPENAI_API_KEY="your-openai-api-key"
```

### PowerShell (Windows)
```powershell
$env:OPENAI_API_KEY="your-openai-api-key"
```

## Usage Examples

```bash
# Interactive mode
azd ai.chat

# Direct query
azd ai.chat "How do I deploy a Node.js app to Azure Container Apps?"

# Azure-specific queries
azd ai.chat "What's the best way to set up CI/CD with Azure DevOps for my web app?"
azd ai.chat "How do I configure Azure Key Vault for my application secrets?"
```

## Azure OpenAI Advantages

- **Enterprise Security**: Your data stays within your Azure tenant
- **Compliance**: Meets enterprise compliance requirements
- **Integration**: Better integration with other Azure services
- **Cost Control**: Better cost management and billing integration
- **Regional Deployment**: Deploy closer to your users for lower latency

## Setup Steps for Azure OpenAI

1. **Create Azure OpenAI Resource**:
   ```bash
   az cognitiveservices account create \
     --name myopenai \
     --resource-group myresourcegroup \
     --location eastus \
     --kind OpenAI \
     --sku s0
   ```

2. **Deploy a Model**:
   - Go to Azure OpenAI Studio
   - Navigate to "Deployments"
   - Create a new deployment with your chosen model (e.g., gpt-35-turbo)
   - Note the deployment name for the environment variable

3. **Get API Key**:
   ```bash
   az cognitiveservices account keys list \
     --name myopenai \
     --resource-group myresourcegroup
   ```

4. **Set Environment Variables** as shown above

## Model Compatibility

The agent supports various GPT models available in Azure OpenAI:
- GPT-3.5-turbo
- GPT-4
- GPT-4-turbo
- And newer models as they become available

Just make sure your deployment name matches the model you want to use.
