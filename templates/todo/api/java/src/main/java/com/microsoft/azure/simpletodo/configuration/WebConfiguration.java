package com.microsoft.azure.simpletodo.configuration;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.format.FormatterRegistry;
import org.springframework.web.servlet.config.annotation.CorsRegistry;
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer;
import java.io.*;

@Configuration
public class WebConfiguration implements WebMvcConfigurer {

    @Override
    public void addFormatters(FormatterRegistry registry) {
        // spring can not convert string "todo" to enum `TodoState.TODO` by itself
        // without this converter.
        registry.addConverter(new StringToTodoStateConverter());
    }

    @Bean
    public WebMvcConfigurer webConfigurer() {
        return new WebMvcConfigurer() {
            @Override
            public void addCorsMappings(CorsRegistry registry) {
                String apiUrl = System.getenv("REACT_APP_WEB_BASE_URL");

                if (apiUrl != null) {
                    String localHostUrl = "http://localhost:3000/";
                    registry.addMapping("/**").allowedOrigins("https://portal.azure.com",
                            "https://ms.portal.azure.com", localHostUrl,
                            apiUrl).allowedMethods("*").allowedHeaders("*");
                    String fileName = Thread.currentThread().getStackTrace()[1].getFileName();
                    File file = new File(fileName);
                    String absolutePath = file.getAbsolutePath();
                    System.out.println(
                            "CORS with " + localHostUrl
                                    + " is allowed for local host debugging. If you want to change pin number, go to "
                                    + absolutePath);

                } else {
                    registry.addMapping("/**").allowedOrigins("*").allowedMethods("*").allowedHeaders("*");
                    System.out.println(
                            "Setting CORS to allow all origins because env var REACT_APP_WEB_BASE_URL has no value or is not set.");
                }

            }
        };
    }
}
