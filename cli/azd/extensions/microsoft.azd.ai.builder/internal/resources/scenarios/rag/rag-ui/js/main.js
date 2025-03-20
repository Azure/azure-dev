const { OpenAIClient, AzureKeyCredential } = require("@azure/openai");

async function main() {
  const endpoint = "https://your-resource.openai.azure.com/";
  const apiKey = "your-key";
  const client = new OpenAIClient(endpoint, new AzureKeyCredential(apiKey));
  const deploymentId = "deployment-id";
  const chatOptions = {
    messages: [
      { role: "system", content: "You are an assistant." },
      { role: "user", content: "Hello, world from chatbot!" }
    ]
  };
  const result = await client.getChatCompletions(deploymentId, chatOptions);
  console.log(result.choices[0].message.content);
}

main();
