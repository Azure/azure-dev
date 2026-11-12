// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"net"
	"net/url"
	"slices"
	"strings"
)

const azurePostgreSQLPlugin = "com.azure.identity.extensions.jdbc.postgresql.AzurePostgresqlAuthenticationPlugin"

type discoveredDatasource struct {
	engine             databaseType
	authentication     databaseAuthentication
	host               string
	port               int
	name               string
	username           string
	password           string
	clientID           string
	clientIDConfigured bool
	sslmode            string
	rootCert           string
	pending            bool
	selectionPending   bool
}

func inferSpringDatasource(
	config *springConfiguration, dependencies springDependencies, requireFinal bool,
) (*discoveredDatasource, error) {
	for _, excluded := range config.properties.list("spring.autoconfigure.exclude") {
		if strings.HasSuffix(excluded, ".DataSourceAutoConfiguration") {
			return nil, configurationError("automatic datasource construction is explicitly disabled")
		}
		if strings.HasSuffix(excluded, ".AzureJdbcAutoConfiguration") {
			dependencies.azureJDBC = false
		}
	}
	pending := !requireFinal
	value := func(key string) (string, error) {
		result, _, err := config.resolve(key, pending)
		return result, err
	}
	properties := map[string]string{}
	for _, key := range []string{
		"url", "username", "password", "driver-class-name", "type",
		"hikari.jdbc-url", "hikari.username", "hikari.password", "hikari.driver-class-name",
		"hikari.data-source-class-name", "hikari.jndi-name", "jndi-name", "azure.passwordless-enabled",
	} {
		resolved, err := value("spring.datasource." + key)
		if err != nil {
			return nil, err
		}
		properties[key] = resolved
	}
	for _, key := range []string{"jndi-name", "hikari.jndi-name", "hikari.data-source-class-name"} {
		if _, present := config.properties.get("spring.datasource." + key); present {
			return nil, configurationError("custom or JNDI datasource construction requires runtime application evidence")
		}
	}
	if properties["type"] != "" && properties["type"] != "com.zaxxer.hikari.HikariDataSource" &&
		(requireFinal || !strings.Contains(properties["type"], "${")) {
		return nil, configurationError("automatic database discovery requires the standard Hikari datasource")
	}
	if config.selectionPending {
		if requireFinal {
			return nil, configurationError("Spring configuration selectors are unresolved")
		}
		return &discoveredDatasource{pending: true, selectionPending: true}, nil
	}
	if !requireFinal {
		for _, key := range []string{
			"url", "hikari.jdbc-url", "driver-class-name", "hikari.driver-class-name", "type",
			"azure.passwordless-enabled",
		} {
			if strings.Contains(properties[key], "${") {
				return &discoveredDatasource{pending: true, selectionPending: true}, nil
			}
		}
		for key, property := range config.properties {
			if strings.HasPrefix(key, compactSpringKey(hikariPropertiesPrefix)+":") ||
				strings.HasSuffix(key, "managedidentityenabled") {
				if strings.HasSuffix(key, "azure.clientId") {
					continue
				}
				resolved, err := config.expand(property.value, true, map[string]bool{})
				if err != nil {
					return nil, err
				}
				if strings.Contains(resolved, "${") {
					return &discoveredDatasource{pending: true, selectionPending: true}, nil
				}
			}
		}
	}
	jdbcURL, username, password, driver := properties["url"], properties["username"], properties["password"],
		properties["driver-class-name"]
	for key, selected := range map[string]*string{
		"hikari.jdbc-url": &jdbcURL, "hikari.username": &username, "hikari.password": &password,
		"hikari.driver-class-name": &driver,
	} {
		if _, present := config.properties.get("spring.datasource." + key); present {
			*selected = properties[key]
		}
	}
	if !dependencies.database && !dependencies.h2 && !dependencies.postgresql && !dependencies.azureJDBC {
		hasDatabaseConfiguration := false
		for key := range config.properties {
			for _, prefix := range []string{
				"spring.datasource.", "spring.jpa.", "spring.jdbc.", "spring.r2dbc.",
				"spring.data.", "spring.flyway.", "spring.liquibase.",
			} {
				hasDatabaseConfiguration = hasDatabaseConfiguration ||
					strings.HasPrefix(key, compactSpringKey(prefix))
			}
		}
		if !hasDatabaseConfiguration {
			return &discoveredDatasource{engine: databaseNone}, nil
		}
	}
	if strings.HasPrefix(jdbcURL, "jdbc:h2:") || (jdbcURL == "" && dependencies.h2 && driver != "org.postgresql.Driver") {
		if !dependencies.h2 || (driver != "" && driver != "org.h2.Driver") {
			return nil, configurationError("the H2 datasource does not match the application runtime dependencies")
		}
		return &discoveredDatasource{engine: databaseH2, authentication: databaseAuthenticationLegacy}, nil
	}
	if !dependencies.postgresql || (driver != "" && driver != "org.postgresql.Driver") {
		return nil, configurationError("a supported datasource and its runtime JDBC driver could not be established")
	}
	if !strings.HasPrefix(jdbcURL, "jdbc:postgresql:") {
		return nil, configurationError("a single PostgreSQL JDBC URL is required for database discovery")
	}
	result := &discoveredDatasource{
		engine: databasePostgreSQL, authentication: databaseAuthenticationPassword,
		host: "localhost", port: 5432, username: username, password: password,
	}
	driverProperties := map[string]string{}
	clientIDConfigured := false
	mapPrefix := compactSpringKey(hikariPropertiesPrefix) + ":"
	for key, property := range config.properties {
		if name, present := strings.CutPrefix(key, mapPrefix); present {
			resolved, err := config.expand(property.value, pending, map[string]bool{})
			if err != nil {
				return nil, err
			}
			driverProperties[name] = resolved
			if name == "azure.clientId" {
				clientIDConfigured = true
			}
		}
	}
	parsed, err := url.Parse(strings.TrimPrefix(jdbcURL, "jdbc:"))
	if err != nil || parsed.User != nil || parsed.Fragment != "" || strings.Contains(jdbcURL, "#") {
		return nil, configurationError("the PostgreSQL JDBC URL is malformed or contains unsupported components")
	}
	urlHost := false
	urlPort := false
	urlDatabase := false
	if parsed.Opaque != "" {
		result.name = parsed.Opaque
		urlDatabase = true
	} else {
		if parsed.Hostname() != "" {
			result.host = parsed.Hostname()
			urlHost = true
		}
		if parsed.Port() != "" {
			result.port, err = springPort(parsed.Port())
			if err != nil {
				return nil, err
			}
			urlPort = true
		}
		result.name = strings.TrimPrefix(parsed.Path, "/")
		urlDatabase = result.name != ""
	}
	parameters, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, configurationError("the PostgreSQL JDBC URL contains invalid driver parameters")
	}
	for key, values := range parameters {
		if len(values) != 1 {
			return nil, configurationError("the PostgreSQL JDBC URL contains repeated driver parameters")
		}
		// pgJDBC URL parameters override the Properties object supplied by Hikari.
		driverProperties[key] = values[0]
		if key == "azure.clientId" {
			clientIDConfigured = true
		}
	}
	if selected, present := driverProperties["PGHOST"]; present && !urlHost {
		result.host = selected
	}
	if selected, present := driverProperties["PGDBNAME"]; present && !urlDatabase {
		result.name = selected
	}
	for key, target := range map[string]*string{"user": &result.username, "password": &result.password} {
		if selected, present := driverProperties[key]; present {
			*target = selected
		}
	}
	if selected, present := driverProperties["PGPORT"]; present && !urlPort {
		result.port, err = springPort(selected)
		if err != nil {
			return nil, err
		}
	}
	if result.name == "" {
		result.name = result.username
	}
	if result.host == "" || strings.ContainsAny(result.host, ", /\\") || strings.Contains(result.name, "/") {
		return nil, configurationError("a single unambiguous PostgreSQL host and database are required")
	}
	for _, field := range []string{result.name, result.username} {
		if strings.Contains(field, "${") && pending {
			result.pending = true
			continue
		}
		if !databaseLoginPattern.MatchString(field) {
			return nil, configurationError("the discovered PostgreSQL database or role name is invalid")
		}
	}
	if !strings.Contains(result.name, "${") && !databaseIdentifierPattern.MatchString(result.name) {
		return nil, configurationError("the discovered PostgreSQL database name is unsupported")
	}
	result.sslmode, result.rootCert = driverProperties["sslmode"], driverProperties["sslrootcert"]
	if result.sslmode == "" {
		if ssl, present := driverProperties["ssl"]; present && (ssl == "" || strings.EqualFold(ssl, "true")) {
			result.sslmode = "verify-full"
		} else {
			result.sslmode = "prefer"
		}
	}
	if pending && strings.Contains(result.sslmode, "${") {
		result.pending = true
	} else if result.sslmode != "verify-full" {
		return nil, configurationError("the PostgreSQL application datasource must use verify-full TLS")
	}
	for _, name := range []string{"service", "socketFactory", "sslfactoryarg", "sslhostnameverifier"} {
		if _, present := driverProperties[name]; present {
			return nil, configurationError("a custom PostgreSQL transport or external service configuration is unsupported")
		}
	}
	if factory := driverProperties["sslfactory"]; factory != "" && factory != "org.postgresql.ssl.LibPQFactory" {
		return nil, configurationError("a custom PostgreSQL TLS factory cannot establish verified application TLS")
	}
	passwordless := false
	if _, present := config.properties.get("spring.datasource.azure.passwordless-enabled"); present {
		passwordless, err = springBoolean(properties["azure.passwordless-enabled"])
		if err != nil {
			return nil, err
		}
	}
	plugin := driverProperties["authenticationPluginClassName"]
	starter := dependencies.azureJDBC && passwordless && strings.TrimSpace(properties["password"]) == ""
	if starter {
		if plugin != "" && plugin != azurePostgreSQLPlugin {
			return nil, configurationError(
				"the Spring Azure integration conflicts with the configured JDBC authentication plugin",
			)
		}
		plugin = azurePostgreSQLPlugin
		for suffix, target := range map[string]string{
			"managed-identity-enabled": "azure.managedIdentityEnabled", "client-id": "azure.clientId",
		} {
			candidate, selectedSource := "", false
			for _, prefix := range []string{"spring.cloud.azure.credential.", "spring.datasource.azure.credential."} {
				selected, present, resolveErr := config.resolve(prefix+suffix, pending)
				if resolveErr != nil {
					return nil, resolveErr
				}
				if present {
					candidate, selectedSource = selected, true
					if suffix == "client-id" {
						clientIDConfigured = true
					}
				}
			}
			if selectedSource {
				if existing, set := driverProperties[target]; set && existing != candidate {
					return nil, configurationError("Spring and JDBC managed identity selectors conflict")
				}
				driverProperties[target] = candidate
			}
		}
		for _, prefix := range []string{"spring.cloud.azure.credential.", "spring.datasource.azure.credential."} {
			for _, suffix := range []string{
				"client-secret", "client-certificate-path", "client-certificate-password",
				"username", "password", "token-credential-bean-name",
			} {
				if _, present := config.properties.get(prefix + suffix); present {
					return nil, configurationError("the Spring Azure integration selects an unsupported credential")
				}
			}
		}
	}
	if plugin != "" {
		if plugin != azurePostgreSQLPlugin || !dependencies.azureIdentity {
			return nil, configurationError("the configured authentication plugin is unsupported or absent at runtime")
		}
		for key := range driverProperties {
			if strings.HasPrefix(key, "azure.") && !slices.Contains([]string{
				"azure.managedIdentityEnabled", "azure.clientId", "azure.scopes",
			}, key) {
				return nil, configurationError("the JDBC authentication configuration selects an unsupported credential")
			}
		}
		managed, err := springBoolean(driverProperties["azure.managedIdentityEnabled"])
		if err != nil || !managed {
			return nil, configurationError("PostgreSQL Entra discovery requires an explicit managed identity credential")
		}
		result.authentication = databaseAuthenticationManagedIdentity
		result.clientID = driverProperties["azure.clientId"]
		result.clientIDConfigured = clientIDConfigured
		if result.password != "" {
			return nil, configurationError("the PostgreSQL datasource supplies both token authentication and a password")
		}
		if result.clientIDConfigured {
			if result.clientID == "" || strings.Contains(result.clientID, "${") {
				if requireFinal {
					return nil, configurationError(
						"the explicitly configured PostgreSQL managed identity client ID is unresolved",
					)
				}
				result.pending = true
			} else if !databasePrincipalIDPattern.MatchString(result.clientID) {
				return nil, configurationError("the PostgreSQL managed identity client ID is invalid")
			}
		}
		if scope := driverProperties["azure.scopes"]; scope != "" && scope != postgresTokenScope {
			return nil, configurationError("the PostgreSQL token audience is unsupported")
		}
	} else {
		if passwordless && strings.TrimSpace(properties["password"]) == "" {
			return nil, configurationError("the passwordless flag has no effective supported authentication integration")
		}
		for key := range driverProperties {
			if strings.HasPrefix(key, "azure.") {
				return nil, configurationError("Azure JDBC credential settings have no effective authentication plugin")
			}
		}
		if result.password == "" {
			return nil, configurationError("password authentication requires the application's actual database password")
		}
		if strings.Contains(result.password, "${") {
			result.pending = true
		}
	}
	if strings.EqualFold(result.host, "localhost") || net.ParseIP(result.host) != nil ||
		strings.Contains(result.host, "${") {
		result.pending = true
	}
	if requireFinal && result.pending {
		return nil, configurationError("the PostgreSQL datasource has unresolved deployment bindings or a local target")
	}
	return result, nil
}
