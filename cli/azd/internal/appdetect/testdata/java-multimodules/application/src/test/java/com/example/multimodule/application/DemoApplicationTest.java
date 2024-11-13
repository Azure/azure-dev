package com.example.multimodule.application;

import static org.assertj.core.api.Assertions.assertThat;

import org.junit.jupiter.api.Test;

import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;

import com.example.multimodule.service.MyService;

@SpringBootTest
public class DemoApplicationTest {

	@Autowired
	private MyService myService;

	@Test
	public void contextLoads() {
		assertThat(myService.message()).isNotNull();
	}

}
