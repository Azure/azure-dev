/*
 * Copyright (c) Microsoft Corporation. All rights reserved.
 * Licensed under the MIT License. See License.txt in the project root for license information.
 */

package com.microsoft.azure.simpletodo.configuration;

import com.azure.core.annotation.Immutable;
import com.azure.core.credential.AccessToken;
import com.azure.core.credential.TokenCredential;
import com.azure.core.credential.TokenRequestContext;
import com.azure.core.exception.ClientAuthenticationException;
import com.azure.core.util.CoreUtils;
import com.azure.core.util.logging.ClientLogger;
import com.azure.core.util.serializer.JacksonAdapter;
import com.azure.core.util.serializer.SerializerAdapter;
import com.azure.core.util.serializer.SerializerEncoding;
import com.azure.identity.CredentialUnavailableException;
import com.azure.identity.implementation.IdentityClientOptions;
import com.azure.identity.implementation.util.LoggingUtil;
import com.azure.identity.implementation.util.ScopeUtil;
import java.io.BufferedReader;
import java.io.File;
import java.io.IOException;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import java.util.regex.Pattern;
import reactor.core.publisher.Mono;

/**
 * A credential provider that provides token credentials based on
 * Azure Developer CLI command.
 */
@Immutable
public class AzureDeveloperCliCredential implements TokenCredential {

    private static final ClientLogger LOGGER = new ClientLogger(AzureDeveloperCliCredential.class);
    private IdentityClientOptions identityClientOptions;

    private static final String WINDOWS_STARTER = "cmd.exe";
    private static final String LINUX_MAC_STARTER = "/bin/sh";
    private static final String WINDOWS_SWITCHER = "/c";
    private static final String LINUX_MAC_SWITCHER = "-c";
    private static final String DEFAULT_WINDOWS_SYSTEM_ROOT = System.getenv("SystemRoot");
    private static final String DEFAULT_MAC_LINUX_PATH = "/bin/";
    private static final String WINDOWS_PROCESS_ERROR_MESSAGE = "'azd' is not recognized";
    private static final Pattern LINUX_MAC_PROCESS_ERROR_MESSAGE = Pattern.compile("(.*)azd:(.*)not found");
    private static final Pattern ACCESS_TOKEN_PATTERN = Pattern.compile("\"token\": \"(.*?)(\"|$)");
    private static final SerializerAdapter SERIALIZER_ADAPTER = JacksonAdapter.createDefaultSerializerAdapter();

    /**
     * Creates an AzureDeveloperCliCredential with default identity client options.
     */
    public AzureDeveloperCliCredential() {
        identityClientOptions = new IdentityClientOptions();
    }

    @Override
    public Mono<AccessToken> getToken(TokenRequestContext request) {
        return authenticateWithAzureDeveloperCli(request)
            .doOnNext(token -> LoggingUtil.logTokenSuccess(LOGGER, request))
            .doOnError(error -> LoggingUtil.logTokenError(LOGGER, identityClientOptions, request, error));
    }

    private Mono<AccessToken> authenticateWithAzureDeveloperCli(TokenRequestContext request) {
        StringBuilder azdCommand = new StringBuilder("azd auth token --output json --scope ");

        var scopes = request.getScopes();

        // It's really unlikely that the request comes with no scope, but we want to
        // validate it as we are adding `--scope` arg to the azd command.
        if (scopes.size() == 0) {
            return Mono.error(LOGGER.logExceptionAsError(new IllegalArgumentException("Missing scope in request")));
        }

        // At least one scope is appended to the azd command.
        // If there are more than one scope, we add `--scope` before each.
        azdCommand.append(String.join(" --scope ", scopes));

        AccessToken token;
        try {
            String starter;
            String switcher;
            if (isWindowsPlatform()) {
                starter = WINDOWS_STARTER;
                switcher = WINDOWS_SWITCHER;
            } else {
                starter = LINUX_MAC_STARTER;
                switcher = LINUX_MAC_SWITCHER;
            }

            ProcessBuilder builder = new ProcessBuilder(starter, switcher, azdCommand.toString());

            String workingDirectory = getSafeWorkingDirectory();
            if (workingDirectory != null) {
                builder.directory(new File(workingDirectory));
            } else {
                throw LOGGER.logExceptionAsError(
                    new IllegalStateException(
                        "A Safe Working directory could not be" + " found to execute Azure Developer CLI command from."
                    )
                );
            }
            builder.redirectErrorStream(true);
            Process process = builder.start();

            StringBuilder output = new StringBuilder();
            try (
                BufferedReader reader = new BufferedReader(
                    new InputStreamReader(process.getInputStream(), StandardCharsets.UTF_8.name())
                )
            ) {
                String line;
                while (true) {
                    line = reader.readLine();
                    if (line == null) {
                        break;
                    }

                    if (
                        line.startsWith(WINDOWS_PROCESS_ERROR_MESSAGE) ||
                        LINUX_MAC_PROCESS_ERROR_MESSAGE.matcher(line).matches()
                    ) {
                        throw LoggingUtil.logCredentialUnavailableException(
                            LOGGER,
                            identityClientOptions,
                            new CredentialUnavailableException(
                                "AzureDeveloperCliCredential authentication unavailable. Azure Developer CLI not installed." +
                                "To mitigate this issue, please refer to the troubleshooting guidelines here at " +
                                "https://aka.ms/azsdk/java/identity/azclicredential/troubleshoot"
                            )
                        );
                    }
                    output.append(line);
                }
            }
            String processOutput = output.toString();

            // wait until the process completes or the timeout (10 sec) is reached.
            process.waitFor(10, TimeUnit.SECONDS);

            if (process.exitValue() != 0) {
                if (processOutput.length() > 0) {
                    String redactedOutput = redactInfo(processOutput);
                    if (redactedOutput.contains("azd login") || redactedOutput.contains("not logged in")) {
                        throw LoggingUtil.logCredentialUnavailableException(
                            LOGGER,
                            identityClientOptions,
                            new CredentialUnavailableException(
                                "AzureDeveloperCliCredential authentication unavailable." +
                                " Please run 'azd login' to set up account."
                            )
                        );
                    }
                    throw LOGGER.logExceptionAsError(new ClientAuthenticationException(redactedOutput, null));
                } else {
                    throw LOGGER.logExceptionAsError(
                        new ClientAuthenticationException("Failed to invoke Azure Developer CLI ", null)
                    );
                }
            }

            LOGGER.verbose(
                "Azure Developer CLI Authentication => A token response was received from Azure Developer CLI, deserializing the" +
                " response into an Access Token."
            );
            Map<String, String> objectMap = SERIALIZER_ADAPTER.deserialize(
                processOutput,
                Map.class,
                SerializerEncoding.JSON
            );
            String accessToken = objectMap.get("token");
            String time = objectMap.get("expiresOn");
            // az expiresOn format = "2022-11-30 02:38:42.000000" vs
            // azd expiresOn format = "2022-11-30T02:05:08Z"
            String standardTime = time.substring(0, time.indexOf("Z"));
            OffsetDateTime expiresOn = LocalDateTime
                .parse(standardTime, DateTimeFormatter.ISO_LOCAL_DATE_TIME)
                .atZone(ZoneId.systemDefault())
                .toOffsetDateTime()
                .withOffsetSameInstant(ZoneOffset.UTC);
            token = new AccessToken(accessToken, expiresOn);
        } catch (IOException | InterruptedException e) {
            throw LOGGER.logExceptionAsError(new IllegalStateException(e));
        } catch (RuntimeException e) {
            return Mono.error(
                e instanceof CredentialUnavailableException
                    ? LoggingUtil.logCredentialUnavailableException(
                        LOGGER,
                        identityClientOptions,
                        (CredentialUnavailableException) e
                    )
                    : LOGGER.logExceptionAsError(e)
            );
        }

        return Mono.just(token);
    }

    private boolean isWindowsPlatform() {
        return System.getProperty("os.name").contains("Windows");
    }

    private String getSafeWorkingDirectory() {
        if (isWindowsPlatform()) {
            if (CoreUtils.isNullOrEmpty(DEFAULT_WINDOWS_SYSTEM_ROOT)) {
                return null;
            }
            return DEFAULT_WINDOWS_SYSTEM_ROOT + "\\system32";
        } else {
            return DEFAULT_MAC_LINUX_PATH;
        }
    }

    private String redactInfo(String input) {
        return ACCESS_TOKEN_PATTERN.matcher(input).replaceAll("****");
    }
}
