# Azure AI Agent - Multi-turn Chat Demo

Your Azure AI Agent now supports two modes:

## 1. Single Query Mode
For one-time questions, pass the query as arguments:
```bash
azd.ai.start.exe "How do I deploy a Node.js app to Azure?"
```

## 2. Interactive Chat Mode
For multi-turn conversations, run without arguments:
```bash
azd.ai.start.exe
```

In interactive mode, you'll see:
- 🤖 Welcome message with instructions
- 💬 You: prompt for your input
- 🤖 AI Agent: responses with context awareness
- Type 'exit' or 'quit' to end the session
- Maintains conversation history for context

### Features:
- ✅ **Context Aware**: Remembers previous messages in the conversation
- ✅ **Azure Focused**: Specialized for Azure development tasks
- ✅ **Easy Exit**: Type 'exit', 'quit', or Ctrl+C to quit
- ✅ **Memory Management**: Keeps last 10 exchanges to prevent context overflow
- ✅ **Error Handling**: Gracefully handles errors and continues the conversation

### Example Interactive Session:
```
🤖 Azure AI Agent - Interactive Chat Mode
Type 'exit', 'quit', or press Ctrl+C to exit
═══════════════════════════════════════════════

💬 You: What is Azure App Service?

🤖 AI Agent: Azure App Service is a platform-as-a-service (PaaS)...

💬 You: How do I deploy to it?

🤖 AI Agent: Based on our previous discussion about App Service...

💬 You: exit

👋 Goodbye! Thanks for using Azure AI Agent!
```

The agent maintains conversation context, so follow-up questions work naturally!
