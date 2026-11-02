# Azure Spring Boot extension

`azure.springboot` packages Maven-based Spring Boot applications for Azure
Container Apps, deploys project-owned Bicep service modules, and prepares
PostgreSQL application access. This is an **unpublished development prototype**:
use azd 1.37.2 or newer and a matching locally built extension bundle. The
version requirement in the examples is not a public release guarantee.

See the [extension design](docs/design.md) for `springbootapp` database,
authentication, and identity settings, their expected usage, and implementation.

## Two packaging paths

- **Explicit `docker.path`**: core azd composes its Docker framework with the
  Java provider. The authored Dockerfile builds the application inside Docker;
  no host Maven/JDK is required. Core builds the image once and the extension
  reuses that package for publish/deploy. The Dockerfile and `.dockerignore`
  remain project-owned.
- **Omitted `docker.path`**: host JDK 17+ and the checked-in Maven Wrapper build
  the JAR, then the extension stages that JAR and a generated runtime recipe in
  an isolated temporary context. An authored Dockerfile or `.dockerignore` can
  remain in place; neither is executed, replaced, or used by this route.

Both paths require Docker; deployment also requires Bicep (azd can install it
when missing). Application SQL and infrastructure remain project-owned.
Automatic discovery and complete application-auth blocks require the native
Maven route. Explicit authored images retain only the earlier manually selected
H2/type-only PostgreSQL routes; their filesystem and launch configuration are
not treated as verified Spring application settings.

## Template compatibility example

The [pinned Weidong REST overlay](test/functional/testdata/spring-petclinic-rest-weidong/README.md)
changes only `azure.yaml` in
`weidongxu-microsoft/petclinic-rest-azd@d8f601e5d3cdbd640069ffde58fc1370541fb0f4`.
The same minimal overlay is published at
[`XiaofeiCao/petclinic-rest-azd@ce7d05f5d2044ce91eedd8c8679d83386b757b39`](https://github.com/XiaofeiCao/petclinic-rest-azd/tree/ce7d05f5d2044ce91eedd8c8679d83386b757b39).
It preserves Bicep, parameter files, scripts, Java sources and Dockerfile:

```yaml
name: petclinic-rest
metadata:
  template: petclinic-rest-azd@0.1.0
requiredVersions:
  azd: ">=1.37.2"
  extensions:
    azure.springboot: ">=0.1.0"
infra:
  provider: bicep
  path: infra
  module: main
services:
  spring-petclinic-rest-app:
    project: .
    language: java
    host: springbootapp
    module: application
```

The extension prepares the original `DATABASE_ADMIN_OBJECT_ID` and
`DATABASE_ADMIN_NAME` inputs. It verifies the selected current ARM deployment
and live resources against `DATABASE_SERVER_ID`, `DATABASE_HOST`,
`APPLICATION_IDENTITY_ID`, `APPLICATION_IDENTITY_CLIENT_ID`,
`APPLICATION_PRINCIPAL_ID`, `CONTAINER_APP_ENVIRONMENT_ID`, and the existing
registry outputs. Saved environment values alone are not ownership evidence.
No new root aliases or resource tags are required. The authored module remains
`infra/application.bicep`; its parameter file retains
`SERVICE_SPRING_PETCLINIC_REST_APP_IMAGE_NAME`. The HTTPS endpoint retains
`/petclinic`, with port 9966 and the original profiles/JDBC managed-identity plugin.
The application selects PostgreSQL, the runtime username, verify-full TLS and
Hikari's managed-identity plugin through its unchanged Spring properties.

> [!WARNING]
> This demo's unchanged Bicep creates `bootstrap-local-ip` from `0.0.0.0` to
> `255.255.255.255` for setup. With the YAML hooks removed, automatic database
> setup takes responsibility for removing that exact current-deployment-owned
> broad rule on success, failure or cancellation and verifying its absence.
> The existing exact runtime-egress rules are verified, not deleted or replaced.
> Entra-only authentication and hostname-verified TLS protect authentication
> and transport, **not source-IP access**. Process-local cleanup cannot guarantee
> recovery after host or credential loss; this is not an automatically expiring
> firewall rule or private networking. Unknown rules are never adopted or deleted.

## Database contract and migration

`database` is directly under `services.<name>`, beside `host`/`language`/`module`.
It crosses the existing protobuf `AdditionalProperties` map; no core schema or
protobuf change is needed. **Omitting `database` selects discovery**, not H2.
The extension reads standard Spring properties/YAML, profiles and imports,
then verifies deployed environment, arguments, secrets and resource bindings.
Packaging/deployment checks use the built Spring Boot JAR's classpath resources
and runtime libraries. H2 requires positive runtime datasource evidence;
test-only H2, missing configuration or a PostgreSQL error never become H2.
An explicitly present empty `database: {}` retains the earlier H2 default.
Existing type-only PostgreSQL configuration retains its legacy
administrator-password route; use a complete auth block for managed access.

Unresolved infrastructure-dependent profile, configuration-location and
datasource selectors remain a typed pending selection during preparation,
without persisting a fabricated engine, identity or readiness marker. Final
resolution must succeed before access setup. The standard local
`file:./config/*/` search visits contained immediate directories in sorted order;
deployed reads never substitute host external files for container files.
Explicit managed-access settings are compared with the effective Spring engine,
authentication, host, port, database, username and password/identity selector.

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
an explicitly selected attached UAMI, Entra-only server authentication and
verify-full TLS. A passwordless flag alone is insufficient.

Official azd 1.37.2 native Maven packaging and the generated Java 17 runtime
have passed local PostgreSQL REST CRUD and restart-persistence checks. A separate
runtime-H2 MVC fixture also passed native packaging and actual H2 database health
without changing its application or dependencies. The PostgreSQL
checks use separate external test configuration with password authentication
and `sslmode=disable`; they do not establish Azure managed identity or
verify-full TLS. A probe using the packaged Spring libraries also covers
repeated command-line options, empty JVM properties and indexed environment
profile groups. Full live lifecycle acceptance remains pending. Earlier
`c78d2cfa` authored-Dockerfile acceptance does not apply to this revision.

This prototype intentionally breaks its earlier syntax:

1. Move `config.database` one level up to `services.<name>.database`.
2. Keep `auth` and `appIdentity` nested as above.
3. Remove `auth.permission`; there is no replacement policy selector.
4. Remove any `bootstrapFirewall` setting. Networking remains authored in IaC.

Old/misplaced, null, malformed and unsupported fields fail validation rather
than silently falling back to H2. Do not retain both database forms.

`managedidentity` maps the attached selected user-assigned identity to an
ordinary non-admin PostgreSQL login; the verified azd developer user remains
the Entra administrator. Password mode is an explicit complete-block alternative:

```yaml
auth:
  type: password
  username: petclinic_app
```

Do not retain `appIdentity` in password mode. `SPRING_DATABASE_PASSWORD` is the
administrator secret and `SPRING_DATABASE_APP_PASSWORD` is the separate app
secret. Reruns reuse secrets; a lost secret is never silently rotated.

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
`POSTGRES_BOOTSTRAP_CLIENT_IP`, where needed for narrow-network templates,
is a troubleshooting-only exact public IPv4 override, not a firewall policy.

## Commands and validation

Use separate `azd init`, `up`, `package`, `deploy`/redeploy and `down` operations.
Local image packaging does not require provisioning. No Azure operations are
part of the offline fixture checks:

```shell
go test ./test/functional -run 'TestSpringPetclinicManagedIdentityFixtureIsPortable|TestSpringPetclinicRest' -count=1
```

The opt-in `AZD_TEST_SPRING_PETCLINIC_PACKAGE=1` native REST gate additionally
starts disposable local PostgreSQL and application containers, checks JSON
CRUD and restart persistence, and removes only its labeled resources and
nonce-scoped image tags. Set `CLI_TEST_AZD_PATH` to the official azd executable.
It requires Docker and a host JDK suitable for the unchanged Maven project.
For a child-session handoff, optionally set `AZD_TEST_SPRING_ARTIFACTS_DIR` to an
existing absolute evidence directory. Each gate retains its exact extension
build-input snapshot and installed bundle in a new private subdirectory there.
These retained artifacts do not establish acceptance if the corresponding gate
fails, and contain no azd configuration or Azure authentication state.

The original [MVC managed-identity fixture](test/functional/testdata/spring-petclinic-managedidentity/README.md)
is retained and migrated to the same application-owned policy. The REST
fixture uses REST APIs, not MVC HTML forms. Offline/source checks are not live
deployment acceptance; that requires separate authorization and owned-resource cleanup.
