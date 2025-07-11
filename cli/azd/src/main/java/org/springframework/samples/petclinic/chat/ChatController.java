package org.springframework.samples.petclinic.chat;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

import org.springframework.samples.petclinic.model.LocalChatMessage;
import org.springframework.messaging.handler.annotation.MessageMapping;
import org.springframework.messaging.handler.annotation.Payload;
import org.springframework.messaging.handler.annotation.SendTo;
import org.springframework.messaging.simp.SimpMessageHeaderAccessor;
import org.springframework.messaging.simp.SimpMessagingTemplate;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.stereotype.Controller;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.core.env.Environment;

import com.azure.ai.openai.OpenAIClient;
import com.azure.ai.openai.OpenAIClientBuilder;
import com.azure.ai.openai.models.ChatChoice;
import com.azure.ai.openai.models.ChatCompletions;
import com.azure.ai.openai.models.ChatCompletionsOptions;
import com.azure.ai.openai.models.ChatRequestMessage;
import com.azure.ai.openai.models.ChatRequestAssistantMessage;
import com.azure.ai.openai.models.ChatRequestSystemMessage;
import com.azure.ai.openai.models.ChatRequestUserMessage;
import com.azure.ai.openai.models.ChatResponseMessage;
import com.azure.identity.DefaultAzureCredentialBuilder;
import com.azure.core.credential.TokenCredential;

/**
 * Created by rajeevkumarsingh on 24/07/17.
 */
@Controller
public class ChatController {

	@Autowired
	private Environment env;

	@MessageMapping("/chat.sendMessageAI")
	@SendTo("/topic/public")
	public LocalChatMessage sendMessageAI(@Payload LocalChatMessage localChatMessage) {
		// sendMessage(localChatMessage);

		System.out.println("Deployment name is: " + env.getProperty("OPENAI_DEPLOYMENT_NAME"));

		TokenCredential defaultCredential = new DefaultAzureCredentialBuilder().build();
		OpenAIClient client = new OpenAIClientBuilder().credential(defaultCredential)
				.endpoint(env.getProperty("OPENAI_DEPLOYMENT_NAME")).buildClient();

		List<ChatRequestMessage> chatMessages = new ArrayList<>();

		String s = localChatMessage.getContent();
		chatMessages.add(new ChatRequestSystemMessage(
				"Assistant is an intelligent chatbot designed to help people find information."));

		chatMessages.add(new ChatRequestUserMessage(s));

		String deploymentName = "gpt-4o-model";
		ChatCompletions chatCompletions = client.getChatCompletions(deploymentName,
				new ChatCompletionsOptions(chatMessages));
		String response = "";
		for (ChatChoice choice : chatCompletions.getChoices()) {

			ChatResponseMessage message = choice.getMessage();
			System.out.println(message.getContent().toString());
			response = message.getContent();
		}

		localChatMessage.setContent(response);
		localChatMessage.setSender("OpenAI");
		return localChatMessage;
	}

	@MessageMapping("/chat.sendMessage")
	@SendTo("/topic/public")
	public LocalChatMessage sendMessage(LocalChatMessage localChatMessage) {
		System.out.println(localChatMessage.getContent());
		return localChatMessage;
	}

	@MessageMapping("/chat.addUser")
	@SendTo("/topic/public")
	public LocalChatMessage addUser(@Payload LocalChatMessage chatMessage, SimpMessageHeaderAccessor headerAccessor) {
		// Add username in web socket session
		headerAccessor.getSessionAttributes().put("username", chatMessage.getSender());
		return chatMessage;
	}

	public void sendComputerMessage(SimpMessagingTemplate template, String message) {
	}

	@GetMapping("/chat")
	public String processForm() {
		return "chat/chat";
	}

}
