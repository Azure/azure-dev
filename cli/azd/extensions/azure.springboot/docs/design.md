# `springbootapp` design

`azure.springboot` provides one automatic Spring Boot workflow for Maven
projects deployed to Azure Container Apps. The extension discovers the
application contract from Spring Boot's effective configuration and verifies
that contract against the current deployment.

## Service contract

A `springbootapp` service uses standard azd fields such as `project`,
`language`, `host`, `module`, resource identity, outputs, environment, hooks,
and dependencies. The extension rejects any authored top-level service property
outside this supported standard azd field set.

Validation uses property presence, so empty and null values are also rejected.
All offending top-level names are sorted and returned in one error without
including values. Validation runs before authentication, environment mutation,
file staging, or Azure operations.

## Packaging

The extension requires a Maven Wrapper project containing:

- `pom.xml`
- `mvnw`
- `.mvn/wrapper/maven-wrapper.properties`
- `src`

The Java framework provider restores and packages the application with the
Maven Wrapper. The service target stages the resulting executable JAR and an
extension-owned Java 17 runtime Dockerfile in a new temporary directory. It
then uses azd's container build and package APIs. Project `Dockerfile` and
`.dockerignore` files are neither read nor modified.

## Datasource discovery

The extension resolves the effective Spring Boot datasource from supported
classpath properties and YAML, profile activation and groups, imports, command
line arguments, JVM properties, environment variables, and deployed Container
App settings. Preparation may retain an unresolved selection when values depend
on infrastructure outputs. Provisioning and deployment require a final,
unambiguous selection.

For PostgreSQL, the extension follows Spring Boot, HikariCP, and pgJDBC
precedence:

1. `spring.datasource.hikari.jdbc-url` overrides
   `spring.datasource.url`.
2. JDBC URL host, port, and database override `PGHOST`, `PGPORT`, and
   `PGDBNAME` driver properties when those URL components are present.
3. JDBC URL query parameters override the Hikari driver property map.
4. Hikari username and password override their generic datasource equivalents.

The result must identify one host and database, use a supported application
authentication mode, and require `sslmode=verify-full` for Azure PostgreSQL.
H2 is selected only from positive runtime evidence; missing or invalid
PostgreSQL configuration never falls back to H2.

PostgreSQL authentication is identified from the effective Spring/pgJDBC
configuration:

- Without an effective supported Azure PostgreSQL authentication plugin, the
  datasource uses password authentication and must resolve the application's
  actual password.
- With
  `com.azure.identity.extensions.jdbc.postgresql.AzurePostgresqlAuthenticationPlugin`
  and `azure.managedIdentityEnabled=true`, the datasource uses managed identity.
- If no `azure.clientId` selector is configured, managed identity means the
  Container App's system-assigned identity.
- If `azure.clientId` is configured and resolves to a valid client ID, managed
  identity means that user-assigned identity.

An explicitly configured client-ID selector that resolves empty or remains
unresolved is invalid; it never falls back to system-assigned identity.
Supplying a password together with the managed-identity plugin is also invalid.

The plugin settings may be supplied directly through Hikari data-source
properties. With the Spring Azure PostgreSQL JDBC starter,
`spring.datasource.azure.passwordless-enabled=true` and the effective
`spring.cloud.azure.credential.*` or
`spring.datasource.azure.credential.*` managed-identity selectors establish
the equivalent authentication intent.

Final discovery verifies the selected identity against the current deployment
and live Container App. System-assigned mode requires the live application
principal to match `APPLICATION_PRINCIPAL_ID`. User-assigned mode additionally
requires the selected identity resource and client ID to match the deployment
outputs and the identity attached to the application.

Recognizable custom datasource factories, custom property sources, bootstrap
registrations, unsupported external configuration, and ambiguous runtime
selectors fail closed because source inspection cannot prove their behavior.

## Deployment binding

The extension accepts only the current project and environment's successful
root deployment and verifies live resource membership rather than trusting
saved environment values. Database, identity, Container App environment,
registry, application, and endpoint values must agree with the verified live
resources.

The authored service module remains project-owned. The extension supplies the
published image, deploys the module through the azd extension contract, and
waits for the live Container App. Endpoint outputs support `_URI`, `_URL`,
`_ENDPOINT`, and `_ENDPOINT_URL`. A selected endpoint must:

- use HTTPS;
- contain no user information, query, or fragment;
- use port 443 when a port is explicit;
- match the live Container App hostname.

The complete path is preserved.

## PostgreSQL access

Administrator connections use hostname-verified TLS and
`search_path=pg_catalog`. Administrative queries qualify catalog functions and
objects where practical. This setting is connection-scoped and does not change
the Spring application's schema search path.

The application role is an ordinary, owned, non-admin login. The fixed
`app-owned-v2` policy grants database `CONNECT`, public-schema `USAGE` and
`CREATE`, table read/write privileges, and sequence usage. It grants no database
ownership, database `CREATE` or `TEMPORARY`, administrator membership, or
elevated role attributes.

For password authentication, reconciliation verifies a real connection using
the exact effective Spring host, database, username, and password after role and
grant checks. An existing role with a different password fails; the extension
does not rotate it. For managed identity, the selected system-assigned or
attached user-assigned identity must match the verified deployment and
PostgreSQL Entra mapping.

The extension creates a temporary narrow bootstrap firewall rule only when
needed and verifies the runtime network path. During `postprovision`, it also
deletes the template-authored `bootstrap-local-ip` rule only when current
deployment evidence proves that the exact rule belongs to the selected
PostgreSQL server and still allows all public IPv4 addresses. This mutation
requires permission to read and delete PostgreSQL firewall rules. The extension
does not adopt or delete unknown, changed, or unowned rules.

Automatic bootstrap address discovery observes the HTTPS egress path through
an external public-address endpoint; it cannot prove which source address a
direct PostgreSQL connection will use. A live split-egress trial produced
different HTTPS and PostgreSQL NAT paths, so the exact discovered rule did not
admit the database connection. `POSTGRES_BOOTSTRAP_CLIENT_IP` supplies an
explicit public IPv4 candidate for troubleshooting, but it does not verify the
PostgreSQL route.

The deletion is bounded by the azd lifecycle. Once `postprovision` is running,
the extension attempts cleanup before returning success or an application,
database, or cancellation error. azd does not invoke `postprovision` when the
provisioning action itself fails. Because Bicep deployments are not
transactional, a PostgreSQL server and broad rule created before a later
failure can remain without extension cleanup. Templates must not treat a broad
bootstrap rule as an automatically recoverable temporary control.

## Failure behavior

Locally checkable configuration errors fail before credentials or mutations.
Missing or conflicting deployment evidence, unsafe TLS, foreign resources,
credential mismatch, elevated roles, and incomplete grants fail explicitly.
Success means the datasource target, application credential, deployment
binding, network/TLS path, and reported endpoint have all been verified.
