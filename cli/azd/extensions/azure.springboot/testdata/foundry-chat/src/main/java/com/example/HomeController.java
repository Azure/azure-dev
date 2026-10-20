package com.example;

import org.springframework.ai.chat.client.ChatClient;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class HomeController {

	private final ChatClient chatClient;

	public HomeController(ChatClient.Builder builder) {
		this.chatClient = builder.build();
	}

	@GetMapping("/")
	public String home() {
		return chatClient.prompt()
			.user("who are you")
			.call()
			.content();
	}
}
