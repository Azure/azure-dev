package com.microsoft.azure.simpletodo;

import com.microsoft.applicationinsights.attach.ApplicationInsights;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

@SpringBootApplication
public class SimpleTodoApplication {

    public static void main(String[] args) {
        ApplicationInsights.attach();

        new SpringApplication(SimpleTodoApplication.class).run(args);
    }
}
