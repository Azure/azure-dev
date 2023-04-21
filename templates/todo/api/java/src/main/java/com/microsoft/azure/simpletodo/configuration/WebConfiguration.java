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

    // For Azure services which don't support setting CORS directly within the service (like Azure Container Apps)
    // You can enable localhost cors access here.
    //    example: localhostOrigin = "http://localhost:3000";
    // Keep empty string to deny localhost origin.
    private static String localhostOrigin = "";

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
                // env.ENABLE_ORYX_BUILD is only set on Azure environment during azd provision for todo-templates
                // You can update this to env.JAVA_ENV if your app is using `development` to run locally and another value
                // when the app is running on Azure (like production or stating)
                String runningOnAzure = System.getenv("ENABLE_ORYX_BUILD");

                if (runningOnAzure != null) {
                    ArrayList<String> origins = new ArrayList<>();
                    origins.add("https://portal.azure.com");
                    origins.add("https://ms.portal.azure.com");

                    if (localhostOrigin != "") {
                        origins.add(localhostOrigin);
                        String fileName = Thread.currentThread().getStackTrace()[1].getFileName();
                        File file = new File(fileName);
                        String absolutePath = file.getAbsolutePath();
                        System.out.println(
                            "Allowing requests from" + localhostOrigin + ". To change or disable, go to " + absolutePath
                        );
                    }

                    // REACT_APP_WEB_BASE_URL must be set for the api service as a property
                    // otherwise the api server will reject the origin.
                    String apiUrlSet = System.getenv("REACT_APP_WEB_BASE_URL");
                    if (apiUrlSet != null) {
                        origins.add(apiUrlSet);
                    }

                    registry
                        .addMapping("/**")
                        .allowedOrigins(origins.toArray(new String[0]))
                        .allowedMethods("*")
                        .allowedHeaders("*");
                } else {
                    registry.addMapping("/**").allowedOrigins("*").allowedMethods("*").allowedHeaders("*");
                    System.out.println("Allowing requests from any origin because the server is running locally.");
                }
            }
        };
    }
}
