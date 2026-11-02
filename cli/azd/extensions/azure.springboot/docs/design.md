# Spring Boot host configuration design

**Status:** unpublished `azure.springboot` 0.1.0 development prototype using
published azd SDK v1.37.2. Official azd native packaging and generated-runtime
PostgreSQL REST CRUD/restart have passed locally using explicit local-only
password/TLS overrides. Selected configuration semantics have actual packaged
Spring-library parity evidence. Live managed-identity/TLS/lifecycle acceptance
remains pending. This is not a public release guarantee.

## Scope and placement

This document covers extension-owned `azure.yaml` settings for services with
`host: springbootapp`: automatic database discovery when `database` is omitted,
and explicit `database` authentication and identity fields. An explicit
`database` belongs directly at `services.<name>.database`, alongside
`host`; it is not nested inside `host` or `config`.

For a complete project example, see the
[`XiaofeiCao/petclinic-rest-azd` template](https://github.com/XiaofeiCao/petclinic-rest-azd/tree/springbootapp-template).
Core azd settings and packaging/deployment-module configuration are outside
this document's scope.

The design separates three concerns: application configuration or explicit YAML selects the database and runtime
account; the extension verifies resources and reconciles access; the application
initializes its own schema. Selecting a resource does not establish ownership,
and successful provisioning does not establish that application tables exist.

## Automatic database discovery

Omitting `database` selects automatic discovery rather than defaulting to H2.
The reader supports standard Spring properties/YAML, profile activation/groups,
contained configuration imports, placeholders, container environment, JVM
properties, JSON and command-line overrides, including indexed environment
profile groups and Spring's comma-joined repeated options. Case-sensitive Hikari JDBC
data-source-property keys remain case-sensitive. Custom/programmatic property
sources and datasource construction are not established by this static reader.

Recognizable custom Java bootstrap mechanisms are rejected by a bounded,
contained source scan that ignores comments, quoted strings and text blocks.
This negative check does not prove arbitrary, generated or transitive Java
behavior. Non-Java bootstrap, mounted application files and custom launch
scripts also require unsupported external/runtime evidence. Automatic discovery
and complete explicit application-auth blocks require native Maven packaging;
authored images retain only manually selected H2/type-only PostgreSQL behavior.

Before provisioning, source configuration supplies intent, not a final deployment
binding. Administrator preparation follows actual nested Bicep parameter
substitutions independently of the final application account. No provisional
database type/access fingerprint or unrelated application password is saved.
Infrastructure-dependent profile/location/datasource selectors may be pending
at this stage, with no guessed engine or authentication. Final unresolved
selectors fail before SQL setup or readiness persistence. The standard local
`file:./config/*/` wildcard reads sorted immediate contained directories with a
128-entry limit; deployed inference never reads host external configuration.

After provisioning, the current successful owned deployment establishes the
actual Container App, its environment/arguments and referenced secrets.
Datasource host, database and selected UAMI must agree with current resource
membership and live identity attachment. Packaging/deployment reads classpath
configuration and runtime dependency evidence from the built Spring Boot JAR,
not stale source classpath files. Passwords resolved from the application remain
in operation-local memory.
Explicit managed-access settings must agree with the effective application
engine, authentication, host/port, database and username, including the selected
UAMI client ID or application password. Current database binding is derived from
the deployment/application, not a saved database name alone.

H2 requires a supported effective H2 datasource and runtime H2 dependency.
PostgreSQL requires its runtime driver, a single unambiguous deployed target,
verify-full TLS and an effective password or supported managed-identity
integration. Test-only H2, a passwordless flag alone, unresolved final bindings,
unsupported construction and errors are not evidence of an H2 datasource.

The automatic route verifies actual outbound addresses against the current
`DATABASE_OUTBOUND_IPS` output and the exact deployment-owned `aca-*` rules.
It registers bounded cleanup of only the verified owned full-IPv4
`bootstrap-local-ip` rule before final inference/setup. Success state is saved
only after deletion and absence verification. Failure/cancellation also invokes
cleanup; unknown rules are not deleted. Process/host loss can still prevent a
local cleanup handler from running.

Sources: [spring_configuration.go](../internal/spring/spring_configuration.go),
[spring_datasource.go](../internal/spring/spring_datasource.go),
[spring_application.go](../internal/spring/spring_application.go),
[spring_provision_inputs.go](../internal/spring/spring_provision_inputs.go),
[spring_deployment_configuration.go](../internal/spring/spring_deployment_configuration.go)
and [automatic_firewall.go](../internal/spring/automatic_firewall.go).

## `database`

Field paths below are relative to `services.<name>`. String fields accept literal
values or azd environment expressions such as `${DATABASE_SERVER_ID}`.
Resolved strings are trimmed before validation.

An optional explicit object selecting database behavior. Supplying an empty
object defaults to H2; omitting the object selects discovery as described above.
Use explicit H2 when no PostgreSQL resources or credentials
are needed. PostgreSQL-specific target/auth fields and PostgreSQL credential
bindings cannot accompany H2.

Decoding is strict: nulls, non-object values, unknown keys, and explicitly empty
string fields are errors, not requests to use defaults. Defaults apply to omitted
fields, not malformed authored values. Legacy `config.database` is rejected even
when the new location is also supplied.

### `database.type`

Selects `h2` or `postgresql`, normalized to lowercase. The default is `h2`.

Use `postgresql` with a complete `auth` object for an ordinary runtime account.
A PostgreSQL block with only `type` and no `auth` retains the legacy
administrator-password route. That compatibility route does **not** provide the
ordinary-account access policy described below. Supplying `serverResourceId` or
`databaseName` without `auth` is rejected.

### `database.serverResourceId`

Required with explicit PostgreSQL authentication; otherwise unsupported.
Identifies the PostgreSQL Flexible Server using a full ARM resource ID containing
subscription, resource group, and server name. A hostname or short server name is
not sufficient.

Use a deployment-output expression when infrastructure creates the server.
This field may remain unresolved before provisioning, but must resolve before
postprovision access reconciliation. A literal ID is also accepted syntactically;
it does not enable adoption of an arbitrary existing server. The selected ID must
match the current deployment and verified live resources.

### `database.databaseName`

Required with explicit PostgreSQL authentication; otherwise unsupported.
Selects the application database on the server. It has no default in this mode.

The resolved value must contain 1-63 ASCII characters, starting with a letter or
underscore and continuing with letters, digits, underscores, or hyphens.
Case is preserved and compared case-sensitively. Unlike server/identity output
IDs, this value must resolve before provisioning.

Use the database selected by the infrastructure, not a schema or connection
string. The extension verifies its scope and access; this field does not cause
application tables or seed data to be created.

Source for database fields and decoding:
[config.go](../internal/spring/config.go), particularly
`decodeDatabaseConfig` and `resolveDatabase`.

### `database.auth`

An optional object for PostgreSQL, but required when authoring target fields.
When present, both `type` and `username` are required. An empty or partial object
is not equivalent to omission.

Choose one explicit mode:

- **Password:** an ordinary application login separate from the server
  administrator. Requires a password-enabled, Entra-disabled server.
- **Managed identity:** an ordinary application login mapped to the selected
  user-assigned managed identity. Requires an Entra-only server with password
  authentication disabled.

These are separate supported paths, not primary/fallback choices. Server
authentication settings are verified against the selection; the extension
does not silently switch modes after a failure.

#### `database.auth.type`

Required when `auth` is present. Accepts `password` or `managedidentity`,
normalized to lowercase, with no default.

For `password`, `SPRING_DATABASE_PASSWORD` supplies the administrator secret and
`SPRING_DATABASE_APP_PASSWORD` supplies the separate application secret.
There is no YAML password field. Saved secrets are reused; missing secrets are
generated only when authoritative checks establish that generating credentials
for a new deployment is safe. Losing a saved secret for existing infrastructure
does not trigger automatic rotation. Persisted secrets belong to the selected
azd environment and must be protected accordingly.

For `managedidentity`, both password variables are rejected, including explicitly
empty bindings. Bootstrap uses the signed-in developer administrator; the
application authenticates with its selected managed identity. The current setup
requires a developer user login, not a service-principal bootstrap flow.

#### `database.auth.username`

Required with either explicit authentication mode. Names the ordinary
application database login, not the administrator, Azure identity display name,
or managed identity client ID.

The resolved value must contain 1-63 ASCII characters, starting with a letter or
underscore and continuing with letters, digits, underscores, periods, `@`, or
hyphens. Case is preserved. Choose a dedicated runtime login; an existing foreign
or elevated role is not automatically adopted.

In password mode this login uses the application secret. In managed identity
mode it maps to the verified identity's principal ID.

#### `database.auth.administratorName`

Optional and supported only with `managedidentity`; rejected in password mode.
Overrides the PostgreSQL administrator login name inferred from the current
developer's login metadata. Use it when that metadata does not expose a usable
name. It must still identify the current signed-in developer, not another
administrator or the runtime application identity.

The resolved name must be nonempty, at most 256 bytes, and contain no control
characters. Conflicting explicit administrator inputs fail instead of overriding
one another silently. The developer's object ID and user access tenant are
resolved and verified independently; this name alone is not identity proof.

Sources: [config.go](../internal/spring/config.go),
[password.go](../internal/spring/password.go),
[developer_identity.go](../internal/spring/developer_identity.go), and
`resolveAdministratorInputs` in [runtime.go](../internal/spring/runtime.go).

#### `database.auth.appIdentity`

Required with `managedidentity`, rejected with `password`, and unsupported for H2.
This object selects the application's runtime identity. Both `type` and
`resourceId` are required; an empty object is invalid.

##### `database.auth.appIdentity.type`

The only supported value is exactly `userAssigned`, with that capitalization.
There is no default and no system-assigned identity option in this contract.

This deliberately binds database access to a specific independently identifiable
Azure resource rather than inferring an identity from a name.

##### `database.auth.appIdentity.resourceId`

Required with managed identity. Selects a UAMI using its full
`Microsoft.ManagedIdentity/userAssignedIdentities` ARM resource ID. Do not supply
its client ID, principal ID, or a display name.

Like `serverResourceId`, it can use a deployment-output expression deferred until
postprovision. The verifier then requires agreement with current outputs,
deployment membership, live UAMI metadata, and the identity attached to the
application. Merely saving an ID in the environment does not establish that
binding.

Sources: `resolveDatabase` in [config.go](../internal/spring/config.go),
[deployment_outputs.go](../internal/spring/deployment_outputs.go), and
`verifySelectedIdentity` in [postgres_access.go](../internal/spring/postgres_access.go).

## Resolution, consistency, and persisted selections

`database` crosses the existing SDK through
`ServiceConfig.AdditionalProperties`; no new protobuf field is introduced.
`decodeDatabaseConfig` enforces field allowlists and required strings.
`resolveDatabase` expands expressions, validates combinations and resource IDs,
and produces one typed `resolvedDatabase` consumed by lifecycle/access code.

Expression substitution prefers the selected azd environment over the process
environment. Missing server/identity output expressions may be deferred before
provisioning; explicitly empty bindings are errors, not deferred values.
Database type, name, auth type, username, and identity type must already resolve.
Mirrored database bindings are consistency checks, not independent overrides:
conflicting values in YAML, saved environment state, or the process fail.

All `springbootapp` services in a project must select compatible database types
and authentication modes. Explicit-auth services must also agree on server,
database name, username, and identity because persisted access state is
environment-scoped, not independently scoped to each service.

The persisted access fingerprint includes policy version, database/auth type,
server ID, database name, username, and identity ID. Changing a provisioned
database type requires a new environment; changing the explicit access selection
requires a new environment or an explicit access migration. Editing readiness
markers is not a supported migration.

Locally checkable configuration conflicts are validated before input writes.
Environment updates use sequential RPCs, not an atomic transaction; a later
failure can leave earlier validated writes. Reruns must revalidate that state.

Source: `resolveService`, `resolveProject`, `compatibleDatabaseSelection`, and
`persistEnvironmentWrites` in [runtime.go](../internal/spring/runtime.go).

## How configuration drives access preparation

**Before provisioning**, the runtime resolves compatible selections and prepares
administrator inputs or safe password inputs. Managed-identity setup resolves
the current developer and correct user access tenant rather than assuming the
subscription resource tenant is suitable for credentials.

**After provisioning**, server/identity expressions must resolve. For explicit
auth, the verifier checks the exact selected successful root ARM deployment,
project/environment tags, current outputs, nested deployment membership, and
live server/database/application/identity bindings. Missing individual environment
tags require proven deployment membership; conflicting tags fail. Saved IDs or
matching tags alone are insufficient.

Access reconciliation then applies fixed **`app-owned-v2`** policy. The runtime
account receives database CONNECT and public-schema USAGE/CREATE and owns objects
it creates, including owner ALTER/DROP powers. It does not own the database,
receive database CREATE/TEMPORARY, or inherit privileged memberships. The
extension rejects administrator-owned or foreign-owned application objects
rather than adopting them. This is not a general shared-database hardening tool.

The administrator connection uses hostname-verified TLS (`sslmode=verify-full`,
TLS 1.2 minimum). For managed identity, bootstrap uses the developer token;
runtime application token acquisition remains the application's responsibility.
The YAML fields select access identities; they do not configure application JDBC
properties or initialize schema.

**Before and after deployment**, handlers require current access-policy state
and reverify bindings/access. Readiness is recorded only after reconciliation;
it is not a durable authorization receipt or proof that application startup SQL
has completed. Spring owns schema/seed execution; there is no separate SQL
initializer or receipt gate.

Sources: [runtime.go](../internal/spring/runtime.go),
[deployment_binding.go](../internal/spring/deployment_binding.go),
[deployment_outputs.go](../internal/spring/deployment_outputs.go),
[azure_credential.go](../internal/spring/azure_credential.go), and
[postgres_access.go](../internal/spring/postgres_access.go).
