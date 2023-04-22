package com.microsoft.azure.simpletodo.configuration;

import java.io.*;
import java.util.ArrayList;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.format.FormatterRegistry;
import org.springframework.web.servlet.config.annotation.CorsRegistry;
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer;

@Configuration
public class WebConfiguration implements WebMvcConfigurer {

    // Use API_ALLOW_ORIGINS env var with comma separated urls like
    // `http://localhost:300, http://otherurl:100`
    // Requests coming to the api server from other urls will be rejected as per
    // CORS.
    private static String allowOrigins = System.getenv("API_ALLOW_ORIGINS");

    // Use API_ENVIRONMENT to change webConfiguration based on this value.
    // For example, setting API_ENVIRONMENT=develop disables CORS checking,
    // allowing all origins.
    private static String environment = System.getenv("API_ENVIRONMENT");

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
                if (environment != null && environment.equals("develop")) {
                    registry.addMapping("/**").allowedOrigins("*").allowedMethods("*").allowedHeaders("*");
                    System.out.println("Allowing requests from any origins. API_ENVIRONMENT=" + environment);
                    return;
                }

                // Enforcing CORS
                ArrayList<String> origins = new ArrayList<>();
                // default Azure origins
                origins.add("https://portal.azure.com");
                origins.add("https://ms.portal.azure.com");

                if (allowOrigins != null) {
                    String[] localhostOrigin = allowOrigins.split(",");
                    String fileName = Thread.currentThread().getStackTrace()[1].getFileName();
                    File file = new File(fileName);
                    String absolutePath = file.getAbsolutePath();

                    for (String origin : localhostOrigin) {
                        origins.add(origin);
                        System.out.println(
                            "Allowing requests from" + origin + ". To change or disable, go to " + absolutePath
                        );
                    }
                }

                registry
                    .addMapping("/**")
                    .allowedOrigins(origins.toArray(new String[0]))
                    .allowedMethods("*")
                    .allowedHeaders("*");
            }
        };
    }
}
