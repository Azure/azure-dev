package com.microsoft.azure.simpletodo;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

@SpringBootApplication
public class SimpleTodoApplication {

    public static void main(String[] args) {
        new SpringApplication(SimpleTodoApplication.class).run(args);
    }
}
