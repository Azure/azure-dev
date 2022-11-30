/*
 * Copyright (c) Microsoft Corporation. All rights reserved.
 * Licensed under the MIT License. See License.txt in the project root for license information.
 */

package com.microsoft.azure.simpletodo.configuration;

import com.mongodb.ConnectionString;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.Arrays;
import java.util.Date;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.autoconfigure.mongo.MongoClientSettingsBuilderCustomizer;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.convert.converter.Converter;
import org.springframework.data.mongodb.core.convert.MongoCustomConversions;

@Configuration
public class MongoDBConfiguration {

    @Bean
    public MongoCustomConversions mongoCustomConversions() {
        return new MongoCustomConversions(
            Arrays.asList(new OffsetDateTimeReadConverter(), new OffsetDateTimeWriteConverter())
        );
    }

    @Bean
    public MongoClientSettingsBuilderCustomizer customizer(
        @Value("${custom.azure.keyvault.secret.mongodb}") String keyVaultSettings
    ) {
        var config = keyVaultSettings.split(",", 0);
        var connString = ConfigurationSources.KeyVaultSecrets.get(config[0], config[1]);
        ConnectionString connection = new ConnectionString(connString);
        return settings -> settings.applyConnectionString(connection);
    }

    static class OffsetDateTimeWriteConverter implements Converter<OffsetDateTime, Date> {

        @Override
        public Date convert(OffsetDateTime source) {
            return Date.from(source.toInstant().atZone(ZoneOffset.UTC).toInstant());
        }
    }

    static class OffsetDateTimeReadConverter implements Converter<Date, OffsetDateTime> {

        @Override
        public OffsetDateTime convert(Date source) {
            return source.toInstant().atOffset(ZoneOffset.UTC);
        }
    }
}
