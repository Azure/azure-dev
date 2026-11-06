// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dev.azd;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@SpringBootApplication
@RestController
public class SmokeApplication {
    private final Environment environment;
    private final JdbcTemplate database;

    public SmokeApplication(Environment environment, JdbcTemplate database) {
        this.environment = environment;
        this.database = database;
    }

    public static void main(String[] args) {
        SpringApplication.run(SmokeApplication.class, args);
    }

    @GetMapping("/")
    public String check() {
        return String.join(",", environment.getActiveProfiles()) + "|"
            + environment.getRequiredProperty("spring.datasource.url") + "|"
            + database.queryForObject("SELECT 42", Integer.class);
    }
}
