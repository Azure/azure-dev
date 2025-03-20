using System;
using Azure;
using Azure.AI.OpenAI;

namespace WebApp {
    class Program {
        static void Main(string[] args) {
            var endpoint = new Uri("https://your-resource.openai.azure.com/");
            var credential = new AzureKeyCredential("your-key");
            var client = new OpenAIClient(endpoint, credential);
            var chatOptions = new ChatCompletionsOptions();
            chatOptions.Messages.Add(new ChatMessage(ChatRole.System, "You are an assistant."));
            chatOptions.Messages.Add(new ChatMessage(ChatRole.User, "Hello, world!"));
            var response = client.GetChatCompletions("deployment-id", chatOptions);
            foreach (var choice in response.Value.Choices) {
                Console.WriteLine(choice.Message.Content);
            }
        }
    }
}
