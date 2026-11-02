# Azure Spring Boot extension

`azure.springboot` packages Maven-based Spring Boot applications for Azure
Container Apps, deploys project-owned Bicep service modules, and prepares
PostgreSQL application access. This is an **unpublished development prototype**:
use azd 1.37.2 or newer and a matching locally built extension bundle. The
version requirement in the examples is not a public release guarantee.

## Automatic application configuration

The extension supports one application path:

- A project contains exactly one `springbootapp` service.
- The checked-in Maven Wrapper builds the Spring Boot JAR with JDK 17 or newer.
- The extension stages the JAR with a generated Java 17 runtime container recipe.
- Docker builds and packages that generated container.
- The extension discovers the effective datasource from Spring configuration
  and verifies it against the deployed Container App and Azure resources.

Do not add `database`, `config.database`, or `docker` to a Spring service in
`azure.yaml`. The extension rejects those blocks, including empty blocks,
before authentication, packaging, provisioning, or environment mutation.
An unreferenced Dockerfile or `.dockerignore` may remain in the project, but the
automatic path does not execute, replace, or use it.

## Template compatibility example

The compatible Petclinic REST template is published at
[`XiaofeiCao/petclinic-rest-azd@ce7d05f5d2044ce91eedd8c8679d83386b757b39`](https://github.com/XiaofeiCao/petclinic-rest-azd/tree/ce7d05f5d2044ce91eedd8c8679d83386b757b39).
It preserves Bicep, parameter files, scripts, Java sources, and its unreferenced
Dockerfile:

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
No new root aliases or resource tags are required.

The authored module remains `infra/application.bicep`; its parameter file
retains `SERVICE_SPRING_PETCLINIC_REST_APP_IMAGE_NAME`. The HTTPS endpoint
retains `/petclinic`, with port 9966 and the original profiles and JDBC
managed-identity plugin.

> [!WARNING]
> This demo's unchanged Bicep creates `bootstrap-local-ip` from `0.0.0.0` to
> `255.255.255.255` for setup. Automatic database setup verifies that this exact
> broad rule belongs to the current deployment but leaves it unchanged. The
> existing exact runtime-egress rules are also verified, not deleted or replaced.
> Entra-only authentication and hostname-verified TLS protect authentication
> and transport, **not source-IP access**. Process-local cleanup cannot guarantee
> recovery after host or credential loss; this is not an automatically expiring
> firewall rule or private networking. Unknown rules are never adopted or deleted.

## Datasource discovery

The extension reads standard Spring properties, YAML, profiles, imports, built
JAR classpath resources, runtime libraries, deployed environment variables,
arguments, secrets, and Azure resource bindings. H2 requires positive runtime
datasource evidence; test-only H2, missing configuration, or a PostgreSQL error
never becomes H2.

Unresolved infrastructure-dependent profile, configuration-location, and
datasource selectors remain pending during preparation. Final resolution must
succeed before access setup. The standard local `file:./config/*/` search visits
contained immediate directories in sorted order; deployed reads never
substitute host external files for container files.

Discovery supports standard Spring Boot datasource auto-configuration, not
arbitrary custom Java configuration. A bounded source check rejects recognizable
custom property sources, bootstrap registrations, and datasource factories
while ignoring comments and string literals. This is not proof of generated or
transitive code, reflection, arbitrary programmatic configuration, mounted
application files, or custom launch scripts.

Automatic PostgreSQL password setup uses the application's effective password
in memory without generating or saving an unrelated application password.
Administrator inputs remain separate and must match the authored infrastructure
bindings. Automatic Entra setup requires an effective supported JDBC integration,
an explicitly selected attached user-assigned managed identity, Entra-only
server authentication, and `sslmode=verify-full`.

## Application-owned startup initialization

All extension-managed application accounts use the fixed **`app-owned-v2`**
access policy: database CONNECT, public-schema USAGE/CREATE, and table read/write
and sequence privileges as applicable. It grants no database CREATE/TEMPORARY,
administrator membership, or database ownership. The application owns objects
it creates, including the ability to ALTER or DROP them.

The extension verifies identity, grants, deployment binding, and connectivity.
Bootstrap readiness does not mean application tables already exist. Spring
startup runs the existing schema and seed SQL with
`spring.sql.init.mode=always`; Hibernate DDL remains `none`. Startup and restart
can rerun SQL and reset or reseed data.

Existing administrator-owned or foreign application objects are rejected, not
adopted or transferred. Old readiness markers are insufficient. Changing the
target, authentication mode, username, or identity requires an explicit
migration. `POSTGRES_BOOTSTRAP_CLIENT_IP`, where needed for narrow-network
templates, is a troubleshooting-only exact public IPv4 override.

## Commands and validation

Use separate `azd init`, `up`, `package`, `deploy` or redeploy, and `down`
operations. Local image packaging does not require provisioning.

The opt-in `AZD_TEST_SPRING_PETCLINIC_PACKAGE=1` gate initializes the fork-owned
template in an empty directory and verifies native packaging. The opt-in
`AZD_TEST_SPRING_PETCLINIC_LIVE=1` gate uses that same template for separately
authorized live lifecycle validation and owned-resource cleanup.
