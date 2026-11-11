# Azure Spring Boot extension

`azure.springboot` packages Maven- and Gradle-based Java Spring Boot applications for Azure
Container Apps, deploys project-owned Bicep service modules, and prepares
PostgreSQL application access. This is an **unpublished development prototype**:
use azd 1.37.2 or newer and a matching locally built extension bundle. The
version requirement in the examples is not a public release guarantee.

See the [extension design](docs/design.md) for automatic datasource discovery,
packaging, deployment, and PostgreSQL access verification.

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
login when the application uses password authentication. The verified azd
developer user remains the Entra administrator. Existing password roles must
authenticate with the effective Spring credential; a mismatch fails without
rotating it.

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
