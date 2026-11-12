# Azure Spring Boot extension

`azure.springboot` packages Maven- and Gradle-based Java Spring Boot applications for Azure
Container Apps, deploys project-owned Bicep service modules, and prepares
PostgreSQL application access. This is an **unpublished development prototype**:
use azd 1.37.2 or newer and a matching locally built extension bundle. The
version requirement in the examples is not a public release guarantee.

See the [extension design](docs/design.md) for automatic datasource discovery,
packaging, deployment, and PostgreSQL access verification.

## Local development installation

Install Go 1.26.4 or newer and azd 1.37.2 or newer. From this extension's directory
in the matching development checkout, build and explicitly install a local bundle:

```shell
azd extension install microsoft.azd.extensions --version 0.14.0
azd x build --skip-install
azd x pack --bundle --output azure-springboot-local.zip
azd extension install azure-springboot-local.zip --force
azd extension show azure.springboot
```

The final command should report `Source: bundle` and the installed version.
No `azd x publish` or local registry setup is needed. `azd x build` alone copies
binaries but does not register a first-time installation.
Use the same azd executable and profile for installation and template initialization.
If `AZD_CONFIG_DIR` is set, use an absolute path so changing directories does not
select another profile.

These commands install the development extension; PostgreSQL Entra bootstrap
also requires its release-configured, digest-pinned MCR runner image. An
unconfigured build does not provide that image or bypass its repository guard.

## Packaging

The host JDK 17+ and checked-in Maven or Gradle Wrapper build the Spring Boot JAR.
Application classes must target Java 17 or earlier; packaging rejects newer
bytecode before container staging. The extension stages the JAR with its Java
17 runtime recipe in an isolated temporary context and sends it through azd's
container pipeline. Project `Dockerfile` and `.dockerignore` files are not read
or modified.

Packaging requires Docker and a supported project at the service's `project`
path. Maven projects require `pom.xml`, `mvnw`,
`.mvn/wrapper/maven-wrapper.properties`, and `src`. Gradle projects require
`build.gradle` or `build.gradle.kts`, `src`, `gradle/wrapper/gradle-wrapper.jar`,
`gradle/wrapper/gradle-wrapper.properties`, and the platform wrapper (`gradlew`
on Unix, `gradlew.bat` on Windows). On Unix, check in the executable bit for
`gradlew`; the extension does not change permissions. A global Gradle installation
is not used. Deployment also requires
Bicep (azd can install it when missing). Application SQL and infrastructure
remain project-owned.

When both build systems are present, **Maven takes precedence**. An invalid Maven
project fails as Maven; it does not fall back to Gradle. Gradle support is limited
to a single root application, not subprojects or composite builds. Kotlin build
scripts are supported for Java applications; Kotlin/Groovy application bootstrap
remains outside the automatic datasource-discovery contract.

The Gradle path resolves production `runtimeClasspath` dependencies, compiles
with `classes` when a build is requested, and packages with `bootJar` without
running tests. Packaging can run without a separate restore or build. It reads
the task's actual archive path, including custom names and contained output
directories; it never chooses a JAR by filename glob or modification time.
Missing, disabled, or skipped `bootJar` tasks fail instead of reusing stale or
plain JARs. If supplied, `dist`/output must identify that exact archive or its
directory; otherwise the extension reports a conflict.

The same standard service entry works with either build system:

```yaml
services:
  app:
    project: .
    language: java
    host: springbootapp
```

Initial validation and provisioning can evaluate the Gradle build to obtain
production dependency evidence before a packaged JAR exists. These wrapper
invocations can download the distribution/dependencies and execute authored
build logic. Temporary extension init scripts and metadata reports are owned
and removed by the extension; authored build files are not changed. Calls use
plain console output, no persistent daemon, and no configuration cache.

## Service configuration and datasource discovery

`springbootapp` is an automatic-only host. A service may use the standard azd
fields needed to identify the project, language, host, module, resource, output,
environment, hooks, and dependencies. Any other authored top-level service
property is rejected before authentication, environment writes, file staging,
or Azure mutations. The error lists offending property names without printing
their values.

The extension reads standard Spring properties/YAML, profiles, imports,
arguments, environment variables, and deployed Container App settings. Final
deployment validation uses the packaged Spring Boot JAR's classpath resources
and runtime libraries. H2 requires positive runtime datasource evidence;
test-only H2, missing configuration, or a PostgreSQL error never becomes H2.
Source-only Gradle checks use resolved production dependencies, including
transitive libraries, not text parsing of the build DSL or test dependencies.

Database-free applications are supported when configuration and dependency
evidence positively establish that no database integration is present. JDBC,
JPA, R2DBC, unsupported database libraries, authored database settings, unresolved
selectors, and incomplete dependency evidence are not treated as database-free.
These apps keep normal Java/container packaging and deployment verification,
but do not require PostgreSQL outputs, administrator inputs, a User login,
the bootstrap image, SQL access, or database firewall preparation. No
`SPRING_DATABASE_TYPE=none` value is exported. A database-free service also
remains independent of PostgreSQL services in the same project.

Unresolved infrastructure-dependent profile, configuration-location, and
datasource selectors remain pending during preparation. Final resolution must
succeed before access setup. The standard local `file:./config/*/` search visits
contained immediate directories in sorted order; deployed reads never
substitute host external files for container files.

Discovery supports standard Spring Boot datasource auto-configuration, not
arbitrary custom Java configuration. A bounded source check rejects recognizable
custom property sources, bootstrap registrations and datasource factories while
ignoring comments/string literals. This is not proof of generated or transitive
code, reflection or arbitrary programmatic configuration. Such customization,
non-Java bootstrap, mounted application files and custom launch scripts require
additional runtime evidence that this prototype does not provide; do not use
automatic or complete-block managed access for those applications.

Automatic PostgreSQL password setup uses the application's effective password
in memory, without generating or saving an unrelated application password.
Administrator inputs remain separate and must match the authored infrastructure
bindings. Automatic Entra setup requires an effective supported JDBC integration,
either the Container App system identity or an explicitly selected attached
UAMI, Entra-only server authentication, and verify-full TLS. An omitted client
ID selects system-assigned identity; an explicitly configured empty or
unresolved client ID is rejected. A passwordless flag alone is insufficient.

Focused datasource and deployment tests cover repeated command-line options,
empty JVM properties, indexed environment profile groups, and both managed
identity modes. Full live lifecycle acceptance remains pending. The extension
maps a verified system-assigned or attached user-assigned identity to an
ordinary non-admin PostgreSQL login, or verifies the effective Spring password
login when the application uses password authentication. A configured User
remains the Entra administrator independently of the deploying User. Existing password roles must
authenticate with the effective Spring credential; a mismatch fails without
rotating it.

### Administrator selection

Start interactive provisioning normally:

```shell
azd up
```

azd selects the environment, subscription, and location through its normal host
experience; no manual subscription `azd env set` is required. For PostgreSQL
Entra authentication, the extension then prompts for a missing User object ID,
defaulting to azd's current User in the selected subscription's resource tenant,
and for the selected User's missing principal name. You can enter another
User's object ID instead of accepting the default. These prompts also apply to
standalone `azd provision`.

Valid configured `DATABASE_ADMIN_OBJECT_ID` and `DATABASE_ADMIN_NAME` values
bypass their respective prompts. A name alone never selects another principal.
The existing
`SPRING_DATABASE_ADMINISTRATOR_NAME`/`SPRING_DATABASE_ADMINISTRATOR_OBJECT_ID`
aliases remain supported; supplied aliases and process/environment values must
agree. Empty/zero/malformed IDs, empty names, placeholders and conflicting
configured inputs fail before identity lookup or extension environment writes.
Invalid prompt responses can be corrected; cancellation saves no partial
administrator selection. Accepted values are persisted for later operations.

The extension does not obtain a name from access-token claims, Microsoft Graph,
Azure CLI, or the object ID. `principalName` is required on the tested PostgreSQL
API path; it is not silently omitted or replaced by a generated label. Prompt
validation checks input format, not directory identity.

With `--no-prompt`, supply the administrator name through the supported
environment inputs before provisioning. An absent object ID uses the host's
current User; supply the object ID too when selecting another User. Missing
names fail deterministically before identity lookup or extension preparation
writes. Native azd subscription/location inputs must also be available for
automation. No-prompt policy comes from azd's SDK, not subprocess TTY detection.
The PostgreSQL Entra path still requires an azd User login; provisioning with
a service principal is not supported on that path.

Supported `preprovision`/`postprovision` lifecycle handlers own preparation;
templates must not duplicate it in `preup` or provision hooks. Normal azd
subscription/location selection remains host-owned. Identity and credential
tenant resolution use host account APIs and the existing azd User login, not a
second Azure CLI login. Saved administrator inputs survive a different deployer;
host-resolved user-access tenant state is refreshed for that deployer's login.
Live verification checks the exact ARM resource/object ID, User type, server
authentication tenant and supplied name; name matching alone is not identity
proof. The template owns resources, sizing and parameter bindings. Fresh
environments need the exact deferred administrator parameter bindings described
in the [design](docs/design.md), because Bicep initializes before
`preprovision` and resolves the prepared values again before deployment.
Non-Spring projects, database-free Spring apps, and H2 do not require
administrator preparation or the bootstrap image. Docker remains necessary
for application-container packaging, not administrator selection.

## Application-owned startup initialization

All extension-managed app accounts use the fixed **`app-owned-v2`** access
policy: database CONNECT, public-schema USAGE/CREATE, and table read/write and
sequence privileges as applicable. It grants no database CREATE/TEMPORARY,
administrator membership or database ownership. The app owns objects it
creates, including the ability to ALTER/DROP those objects. This replaces the
old DML-only guarantee.

The extension verifies identity, grants, deployment binding and connectivity;
bootstrap readiness does **not** mean application tables already exist.
Spring startup runs the existing schema/seed SQL with
`spring.sql.init.mode=always`; Hibernate DDL remains `none`. There is no separate
initializer, receipt, baseline-once promise or initialization completion gate.
Startup/restart can rerun SQL and reset/reseed data; the upstream SQL is not
claimed to be perfectly idempotent.

Existing administrator-owned or foreign application objects are rejected,
not adopted or transferred. Old DML-only readiness markers are insufficient.
Use a fresh database/environment or a separately approved migration. Changing
target, auth mode, username or identity likewise needs explicit migration.
For automatic managed-identity deployments, the extension runs access
reconciliation in a short-lived Container Apps Job in the application's
verified environment. The job must report exactly the outbound IPv4 addresses
already covered by deployment-owned PostgreSQL firewall rules before the
extension grants its system identity temporary PostgreSQL administrator access.
The job has no secrets, private registry credentials, user-assigned identities,
retries, or caller-supplied template override. The extension removes the
temporary administrator before deleting the job.

Failed executions retain only the unprivileged job for log inspection. A later
run recovers verified stale jobs by removing any matching administrator first.
If administrator removal cannot be confirmed, the job is retained so the
identity/administrator pair can be recovered deterministically. This path
requires permission to manage Container Apps Jobs and PostgreSQL Entra
administrators in addition to reading the verified application and firewall
resources. Release builds enable this path only by injecting a digest-pinned
runner from `mcr.microsoft.com/azure-dev/azd-spring-postgres-bootstrap`.
The build scripts accept it through the release-only
`POSTGRES_BOOTSTRAP_IMAGE` input. Builds without that Microsoft-owned image
fail before authentication or Azure mutation rather than falling back to an
untrusted or validation-only image.

Password-authenticated and unsupported custom deployments retain local
reconciliation. `POSTGRES_BOOTSTRAP_CLIENT_IP` remains a troubleshooting-only
exact public IPv4 override for that local path. Legacy deployments containing
the exact template-authored `bootstrap-local-ip` rule are still cleaned up only
after deployment ownership and the unchanged unrestricted range are verified.
New templates must not create that broad rule.

## Commands and validation

Use separate `azd init`, `up`, `package`, `deploy`/redeploy and `down`
operations. Local image packaging does not require provisioning. The extension
test suite is self-contained and does not vendor application repositories:

```shell
go test ./...
```

The opt-in local Gradle package gate uses checksum-verified Gradle 8.14.4 wrappers
and Spring Boot 3.5.13 Java fixtures for both build DSLs. Set
`AZD_TEST_SPRING_GRADLE_PACKAGE=1` and `CLI_TEST_AZD_PATH` to an official azd
1.37.2+ executable, provide JDK 17+ and a running Docker engine, then run:

```shell
go test ./test/functional -run '^Test_CLI_SpringGradle_Package$' -count=1 -timeout 35m
```

This gate initializes isolated projects, packages twice, verifies the exact
`bootJar` artifact, and starts the generated image with healthy H2 and non-root
execution. It downloads public build dependencies but does not deploy to Azure.
Current real Gradle packaging acceptance is on Windows with JDK 21 compiling
Java 17 application classes. Unix wrapper execution and Azure Gradle deployment
remain unverified.
