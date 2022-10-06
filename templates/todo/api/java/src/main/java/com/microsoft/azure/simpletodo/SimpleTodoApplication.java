package com.microsoft.azure.simpletodo;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

import com.microsoft.applicationinsights.attach.ApplicationInsights;

@SpringBootApplication
public class SimpleTodoApplication {

    public static void main(String[] args) {
        ApplicationInsights.attach();

        new SpringApplication(SimpleTodoApplication.class).run(args);
    }
}
