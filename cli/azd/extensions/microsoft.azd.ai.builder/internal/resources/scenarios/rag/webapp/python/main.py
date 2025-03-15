from azure.ai.openai import OpenAIClient
from azure.core.credentials import AzureKeyCredential

def main():
    endpoint = "https://your-resource.openai.azure.com/"
    api_key = "your-key"
    client = OpenAIClient(endpoint, AzureKeyCredential(api_key))
    deployment_id = "deployment-id"
    response = client.get_chat_completions(deployment_id, messages=[
        {"role": "system", "content": "You are an assistant."},
        {"role": "user", "content": "Hello, world!"}
    ])
    print(response.choices[0].message.content)

if __name__ == "__main__":
    main()
