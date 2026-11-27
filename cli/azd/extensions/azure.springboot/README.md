# Azure Spring Boot extension

`azure.springboot` packages Maven-based Java Spring Boot applications for Azure
Container Apps, deploys project-owned Bicep service modules, and prepares
Entra-authenticated PostgreSQL application access. This extension is currently
in **preview**: use azd 1.37.2 or newer and install the extension before
initializing or deploying a `host: springbootapp` project.

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

These commands provide the complete development extension. PostgreSQL setup
runs locally during provisioning; no bootstrap image, container Job, image
publication, or release-only build input is required.

## Packaging

The host JDK 17+ and checked-in Maven Wrapper build the Spring Boot JAR.
Application classes must target Java 17 or earlier; packaging rejects newer
bytecode before container staging. The extension stages the JAR with its Java
17 runtime recipe in an isolated temporary context and sends it through azd's
container pipeline. Project `Dockerfile` and `.dockerignore` files are not read
or modified.

Packaging requires Docker and a supported Maven Wrapper project at the service's
`project` path. Maven services require `pom.xml` and `src` at the service path,
with `mvnw` and `.mvn/wrapper/maven-wrapper.properties` at the service path or a
contained ancestor directory within the azd project (supporting multi-module
Maven reactors). Deployment also requires Bicep (azd can install it when
missing). Application SQL and infrastructure remain project-owned.

```yaml
services:
  app:
    project: .
    language: java
    host: springbootapp
```

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

Database-free applications are supported when configuration and dependency
evidence positively establish that no database integration is present. JDBC,
JPA, R2DBC, unsupported database libraries, authored database settings, unresolved
selectors, and incomplete dependency evidence are not treated as database-free.
These apps keep normal Java/container packaging and deployment verification,
but do not require PostgreSQL outputs, administrator inputs, a User login,
SQL access or database firewall preparation. No
`SPRING_DATABASE_TYPE=none` value is exported. A database-free service also
remains independent of PostgreSQL services in the same project.

Unresolved infrastructure-dependent profile, configuration-location, and
datasource selectors remain pending during preparation. Final resolution must
succeed before access setup. `spring.config.import: [optional:]configserver:...`
is honored when `spring.cloud.config.enabled=false`, when a module has no
database dependencies, or when higher-precedence Container App environment /
launch overrides explicitly supply `spring.datasource.url` and `username`.
Multiple PostgreSQL `springbootapp` services in one project may target distinct
databases, roles, and managed identities on the same provisioned PostgreSQL
Flexible Server (distinct identities cannot share one PostgreSQL role login).
The standard local `file:./config/*/` search visits
contained immediate directories in sorted order; deployed reads never
substitute host external files for container files.

Discovery supports standard Spring Boot datasource auto-configuration, not
arbitrary custom Java configuration. A bounded source check rejects recognizable
custom property sources, bootstrap registrations and datasource factories while
ignoring comments/string literals. This is not proof of generated or transitive
code, reflection or arbitrary programmatic configuration. Such customization,
non-Java bootstrap, mounted application files and custom launch scripts require
additional runtime evidence that this extension does not provide; do not use
automatic or complete-block managed access for those applications.

PostgreSQL datasources must use Entra managed-identity authentication. Automatic
Entra setup requires an effective supported JDBC integration, either the
Container App system identity or an explicitly selected attached UAMI,
Entra-only server authentication, and verify-full TLS. An omitted client ID
selects system-assigned identity; an explicitly configured empty or unresolved
client ID is rejected. Password-authenticated PostgreSQL datasources and a
passwordless flag without an effective plugin are rejected.

Focused datasource and deployment tests cover repeated command-line options,
empty JVM properties, indexed environment profile groups, and both managed
identity modes. Live lifecycle acceptance requires a separate Azure run. The
extension maps a verified system-assigned or attached user-assigned identity to
an ordinary non-admin PostgreSQL login. A configured User remains the Entra
administrator independently of the deploying User.

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
agree. Unsupported password variables (`SPRING_DATABASE_PASSWORD` and
`SPRING_DATABASE_APP_PASSWORD`) are rejected so credentials never silently
coexist with Entra managed-identity authentication. Empty/zero/malformed IDs,
empty names, placeholders and conflicting configured inputs fail before identity
lookup or extension environment writes. Invalid prompt responses can be
corrected; cancellation saves no partial administrator selection. Accepted
values are persisted for later operations.

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
host-resolved user-access tenant state (`SPRING_DATABASE_USER_ACCESS_TENANT_ID`)
is refreshed for that deployer's login and must not be set manually or copied
across environments.
Live verification checks the exact ARM resource/object ID, User type, server
authentication tenant and supplied name; name matching alone is not identity
proof. The template owns resources, sizing and parameter bindings. Fresh
environments need the exact deferred administrator parameter bindings described
in the [design](docs/design.md), because Bicep initializes before
`preprovision` and resolves the prepared values again before deployment.
Non-Spring projects, database-free Spring apps, and H2 do not require
administrator preparation. Docker remains necessary
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
For automatic managed-identity deployments, only `postprovision` performs
privileged SQL. The extension verifies the current deployment and public
PostgreSQL endpoint, temporarily opens an owned firewall rule covering
`0.0.0.0` through `255.255.255.255`, and connects from the local machine using
hostname-verified TLS. The local network must permit outbound TCP 5432;
a firewall rule cannot bypass a corporate network restriction or Azure Policy.
This intentionally permits any IPv4 source during bootstrap; Entra
authentication and TLS remain required.

The selected User remains the persistent administrator. An existing caller
administrator is verified and its actual login alias is used without changing
or deleting that registration. Otherwise, the extension temporarily registers
the signed-in User with a separate session login. It removes only that owned
registration after closing SQL sessions. The app keeps its ordinary managed
identity login and exact Container Apps runtime firewall rules.

Both temporary resources are cleaned up with bounded, cancellation-independent
operations, and their absence is checked before readiness is saved. Cleanup
failure fails provisioning. Ownership records and a local process lock under
`.azure/spring-postgres-bootstrap/` support recovery by a later `azd provision`
from the same project. Do not remove that state while cleanup is outstanding.
A killed process can leave access open: there is no automatic expiration, and
an accepted firewall deletion is not an instantaneous enforcement boundary.
Changed or unowned resources are not deleted.

`predeploy` checks current provisioning readiness, deployment/datasource
bindings, and runtime network policy without administrator SQL. There is no
postdeploy SQL handler or immediate post-startup SQL ownership audit; rerun
`azd provision` for reconciliation. Legacy deployments containing the exact
template-authored `bootstrap-local-ip` rule are cleaned up only after deployment
ownership and the unchanged unrestricted range are verified. New templates must
not create that broad rule.

## Commands and validation

Use separate `azd init`, `up`, `package`, `deploy`/redeploy and `down`
operations. Local image packaging does not require provisioning. The extension
test suite is self-contained and does not vendor application repositories:

```shell
go test ./...
```
