# `springbootapp` design

`azure.springboot` provides one automatic Spring Boot workflow for Maven and
single-project Gradle Java applications deployed to Azure Container Apps. The extension discovers the
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

Build-system detection prefers Maven whenever `pom.xml` exists; malformed Maven
input does not trigger a fallback to Gradle. Otherwise, `build.gradle` or
`build.gradle.kts` selects the extension-local Gradle adapter. Other Java hosts
retain native Maven behavior, and core existing-code discovery is unchanged.

A Maven Wrapper project contains:

- `pom.xml`
- `mvnw`
- `.mvn/wrapper/maven-wrapper.properties`
- `src`

A Gradle project contains a build script, `src`, the platform wrapper
(`gradlew` with executable permission on Unix or `gradlew.bat` on Windows),
`gradle/wrapper/gradle-wrapper.properties`, and `gradle/wrapper/gradle-wrapper.jar`
at its configured service root. Wrapper inputs, scripts, and archive outputs
must be contained regular files, not links. Gradle's normal build-file selection
applies when both build DSL files exist. The evaluated model must contain one
root project without subprojects or included builds.

The Java framework provider keeps native Maven restore/build/package behavior.
For Gradle, an extension-owned temporary init script registers a metadata task.
Restore strictly resolves production `runtimeClasspath`; build also invokes
`classes`; package invokes `bootJar` with tests excluded and is self-sufficient.
A fresh bounded JSON report supplies the evaluated dependency coordinates and
`bootJar.archiveFile`, avoiding build-DSL regexes, console parsing, output globs,
and stale/plain-JAR selection. Missing or disabled tasks, failed dependency
resolution, skipped packaging, unsafe paths, and malformed reports fail
explicitly. Valid up-to-date or build-cache-restored output is verified normally.
Custom contained archive names/directories are supported. Standard `dist`/output,
when supplied, must agree with the task's archive or its directory.

Gradle commands inherit the build environment, propagate cancellation, and use
plain console output with no persistent daemon or configuration cache. Wrapper
execution is a deliberate boundary after static service/file validation: it can
download dependencies and execute authored build logic, including during
source-only datasource discovery. It does not modify authored configuration or
write azd state to manufacture dependency evidence. Metadata is not persisted
across commands. Errors use the existing URL non-disclosure boundary.

Both paths normalize the selected executable JAR to a local package directory
containing `app.jar`, with `framework: maven` or `framework: gradle` metadata.
Consumers require a single package artifact matching the selected build system.
Gradle additionally verifies the executable Spring Boot manifest and archive
layout. The service target stages the resulting executable JAR and an
extension-owned Java 17 runtime Dockerfile in a new temporary directory. It
then uses azd's container build and package APIs. Project `Dockerfile` and
`.dockerignore` files are neither read nor modified. Application classes must
target Java 17 or earlier; packaging fails before container staging when the
JAR contains newer application bytecode.

## Datasource discovery

The extension resolves the effective Spring Boot datasource from supported
classpath properties and YAML, profile activation and groups, imports, command
line arguments, JVM properties, environment variables, and deployed Container
App settings. Preparation may retain an unresolved selection when values depend
on infrastructure outputs. Provisioning and deployment require a final,
unambiguous selection.

Without a package context, Maven dependency discovery reads its supported POM
declarations; Gradle dependency discovery evaluates the production runtime
classpath, including transitive libraries. Test-only dependencies are not
datasource evidence. With a package context, the actual JAR resources/libraries
remain authoritative for both build systems. Kotlin build DSL support does not
expand the existing Java-only application-bootstrap analysis.

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

Automatic managed-identity deployments reconcile PostgreSQL access in a
short-lived Container Apps Job in the application's verified managed
environment. This avoids inferring the database TCP source address from an
unrelated local HTTPS request. Before creating a temporary PostgreSQL
administrator, the extension reads the job back and verifies:

- the expected environment, ownership tags, location, and system identity;
- no secrets, private registries, user-assigned identities, retries, init
  containers, volumes, command, or caller-supplied execution override;
- the fixed digest-pinned image, input contract, timeout, and single-replica
  template;
- an outbound IPv4 set exactly equal to the application's deployment-owned
  `aca-<ip>` firewall-rule inventory.

The job receives only a versioned, non-secret input value. Its managed identity
acquires a token for Azure Database for PostgreSQL and connects with
hostname-verified TLS. The temporary session administrator is used only for the
connection. Database and initialization-marker ownership checks continue to
expect the deployment-verified developer administrator, preserving the
`app-owned-v2` policy.

The extension creates the job and its PostgreSQL administrator dynamically
after Bicep succeeds; neither resource is part of the authored template. It
starts the stored template without an execution override and polls the exact
returned execution. On success, cancellation before execution, or start
failure, cleanup removes the administrator first and then the job. A failed
execution removes the administrator but temporarily retains the unprivileged
job for log inspection. Later runs recover only stale jobs whose complete
template, tags, identity, server, environment, and input contract can still be
proved; administrator deletion must succeed before job deletion. An
administrator whose matching owned job no longer exists is never inferred or
deleted.

Password-authenticated and unsupported custom deployments retain local
reconciliation. That path can create a temporary exact firewall rule and can
accept `POSTGRES_BOOTSTRAP_CLIENT_IP` as a troubleshooting-only public IPv4
candidate. HTTPS discovery cannot prove the direct PostgreSQL route, so it is
not used for automatic managed-identity bootstrap.

Legacy automatic deployments may still contain the exact template-authored
`bootstrap-local-ip` rule. The extension deletes it only when current deployment
evidence proves ownership and the range remains unrestricted. Unknown, changed,
or unowned rules are never deleted. New templates must provision only the exact
Container Apps outbound rules. PostgreSQL remains on a public endpoint; this
design removes broad bootstrap exposure and split local egress dependence but
does not provide private networking.

For automatic managed-identity provisioning, azd initializes the Bicep provider
before it invokes the extension's `preprovision` event. A template that maps
required Bicep parameters directly to unset `DATABASE_ADMIN_*` values therefore
fails before the extension can resolve the signed-in developer. Supported
templates use this exact deferred binding pair in `main.parameters.json`:

```json
{
  "databaseAdministratorName": {
    "value": "${DATABASE_ADMIN_NAME=azd-preprovision-pending}"
  },
  "databaseAdministratorObjectId": {
    "value": "${DATABASE_ADMIN_OBJECT_ID=00000000-0000-0000-0000-000000000000}"
  }
}
```

The placeholders satisfy only the provider's initial required-input check.
Before the deployment plan is created, `preprovision` resolves the current
developer, persists the real values, and azd reloads the parameter file. The
extension rejects mixed or different fallback bindings. If the lifecycle event
does not run, the zero principal causes deployment to fail closed rather than
configure a different administrator.

Release builds enable remote reconciliation only with a digest-pinned runner
from `mcr.microsoft.com/azure-dev/azd-spring-postgres-bootstrap`. The release
pipeline supplies the published digest through the build-only
`POSTGRES_BOOTSTRAP_IMAGE` input and must publish that image with vulnerability
scanning, SBOM, signing, provenance, servicing, and release retention. Builds
without the release image fail before authentication or Azure mutation;
validation-only or personal registry images are not accepted.

## Failure behavior

Locally checkable configuration errors fail before credentials or mutations.
Missing or conflicting deployment evidence, unsafe TLS, foreign resources,
credential mismatch, elevated roles, and incomplete grants fail explicitly.
Success means the datasource target, application credential, deployment
binding, network/TLS path, and reported endpoint have all been verified.
