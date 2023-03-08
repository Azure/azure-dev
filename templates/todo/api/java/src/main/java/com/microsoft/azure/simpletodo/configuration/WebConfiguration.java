package com.microsoft.azure.simpletodo.configuration;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.format.FormatterRegistry;
import org.springframework.web.servlet.config.annotation.CorsRegistry;
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer;

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
                var apiUrl = System.getProperty("REACT_APP_WEB_BASE_URL");

                if (apiUrl != "") {
                    registry.addMapping("/**").allowedOrigins("https://portal.azure.com",
                            "https://ms.portal.azure.com",
                            apiUrl).allowedMethods("*").allowedHeaders("*");
                } else {
                    registry.addMapping("/**").allowedOrigins("*").allowedMethods("*").allowedHeaders("*");
                    System.out.println("error: could get environment variable REACT_APP_WEB_BASE_URL");
                }

            }
        };
    }
}
