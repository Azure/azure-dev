# Spring Boot (Beta)

Beta extension for `azd spring init`. It infers a Spring Boot application's
active database and supported Spring AI Azure OpenAI chat or embedding binding, then generates
an azd project without requiring an existing `azure.yaml`.
It is not yet published to the extension registry.

See [DESIGN.md](DESIGN.md) for the resource model and configuration-resolution contract.

## Try it locally

From this extension directory, build and register a local bundle:

```sh
azd x build
azd x pack --bundle --output ./bin/bundle
azd extension install ./bin/bundle/azure-springboot_1.0.0-beta.1.zip --force
```

Then, from a **trusted application directory**:

```sh
azd spring init
azd up
```

For scripts, use `azd spring init --yes --no-prompt`. `--yes` consents to executing
the application's Maven wrapper/build extensions and accepts the generation plan.
Without it, initialization asks for trust before Maven inspection and confirms
the inferred resources/files before writing. `init` itself does not create an azd
environment, provision resources, or deploy the application.

Application configuration supplies deployment defaults. Use `--profiles postgres`
to select alternatives explicitly. Ambient variables such as `SPRING_PROFILES_ACTIVE`,
`SPRING_DATASOURCE_URL`, and `SPRING_APPLICATION_JSON` do not influence analysis.
Unresolved profile selectors prompt with discovered profile choices; required
datasource URL/port placeholders prompt for values. Resolved defaults need no extra questions.
With `--no-prompt`, select profiles through `--profiles` and resolve required
placeholder values in application configuration. Errors identify missing inputs
and their sources; there is no generic property-override option.
Other unresolved configuration requires application changes. `--yes` accepts
confirmations, not missing choices. Interactive prompts use the azd host channel.

Declare application-owned runtime inputs explicitly:

```sh
azd spring init \
  --application-input FRONTEND_URL=value \
  --application-input JWT_SECRET=secret
azd env set FRONTEND_URL "https://example.com"
azd env set-secret JWT_SECRET
azd up
```

The flag classifies an environment variable; it never accepts its value.
`value` inputs become ordinary Container Apps environment variables. `secret`
inputs use secure Bicep parameters, Container Apps secrets, and `secretRef`
bindings. Required values must exist in the selected azd environment before
provisioning. Input names are shown during initialization, but values are not.
Deployment-owned Spring, datasource, and Foundry variable names are rejected.
Global Spring and JVM configuration override channels are also rejected; the
complete ownership contract is in [DESIGN.md](DESIGN.md#appendix-application-input-ownership-and-conflicts).
Application inputs do not modify source or infer additional Azure resources.
This unpublished feature currently depends on unresolved core azd support
tracked in [#10688](https://github.com/Azure/azure-dev/issues/10688),
[#10691](https://github.com/Azure/azure-dev/issues/10691), and
[#10692](https://github.com/Azure/azure-dev/issues/10692). See
[DESIGN.md](DESIGN.md) for the release boundary.

PostgreSQL managed identity is inferred from supported passwordless Spring
properties or the Azure PostgreSQL JDBC authentication plugin. MySQL managed
identity is deferred.

## Initial scope

Single-module Maven applications using Spring Boot executable JAR packaging and
Java 17 or newer. Analysis reads effective Maven metadata, conventional
`application*.properties`/YAML configuration, active/default profiles,
includes/groups, property placeholders, and explicit deployment inputs.
Configuration lists support strings, numbers, and booleans; complex elements remain unsupported.
Inactive database alternatives are not provisioned.
Includes accumulate across property sources; profile groups expand depth-first
in activation order, preserving Spring precedence.
JDBC dependencies establish available drivers, not active database requirements.
The Spring AI Azure OpenAI starter plus active standard endpoint, API-key, and
modality configuration establishes a Foundry binding. Chat remains the legacy
default when only shared Azure OpenAI properties are configured. Embeddings require
an explicit Azure OpenAI embedding selector or deployment-name property.
An active non-empty API-key property selects API-key authentication. If the
property is absent or empty, Spring AI uses `DefaultAzureCredential`, and the
extension selects managed identity.
Resolved explicit JDBC driver classes are pinned with deployment URLs; omitted
drivers retain Spring's automatic selection. Driver availability uses Maven's
resolved runtime dependency graph, including transitive dependencies.
Repackaging checks include plugin defaults and active execution configuration.
The resolved Maven build directory and Boot `repackage.outputDirectory` determine
the JAR copy path; project-local custom output directories, including spaces and
inherited settings, are supported. Relative repackage paths use the project root.
Active repackage executions must agree on one executable-JAR output directory.
Missing/unresolved paths, directories outside the Docker context, and output
directories overlapping application inputs or generated files are rejected.
Hikari, Tomcat, or DBCP2 pool-specific connection bindings, including empty values, are
rejected; use the common `spring.datasource` connection settings instead.
Ordinary pool tuning is supported.
The normalized Maven artifact ID must start with a letter, contain no consecutive
hyphens, and be at most 28 characters so the derived `<name>-app` fits Azure naming rules.

| Active datasource | Generated database |
| --- | --- |
| In-memory H2, Derby, or HSQLDB | None |
| MySQL JDBC URL | Azure Database for MySQL server and the inferred database |
| PostgreSQL JDBC URL | Azure Database for PostgreSQL server and the inferred database |

A Foundry binding provisions one `AIServices` account and an account-level model
deployment for each detected modality. It does not require a Foundry project,
Search, or Storage. Chat defaults to `gpt-5.6-sol` version `2026-07-09`.
Embeddings default to `text-embedding-3-small` version `1`. Both use
`GlobalStandard`, and Azure upgrades them when the current version expires.

Foundry modality detection honors `spring.ai.model.chat` and
`spring.ai.model.embedding`. An explicitly configured Azure OpenAI deployment is
rejected when its corresponding selector chooses another provider.
`spring.ai.azure.openai.openai-api-key` must resolve empty because a non-empty
value selects public OpenAI instead of the provisioned Azure account. Generated
runtime bindings keep this property empty.

Hosting uses Azure Container Apps, a container registry, and Log Analytics.
The current deployment supports HTTP web applications. Explicit non-web mode and
HTTPS-only application servers require different hosting contracts and are rejected.
Gradle, multi-module/custom packaging, custom datasource-producing beans, programmatic
profile activation, custom configuration locations/imports, and other resource
types require further analysis and are explicitly rejected when detected.
Ordinary beans consuming an injected datasource or connection factory are supported.
File-based embedded databases require a persistence policy and are not adopted.

## Generated files and boundaries

- `azure.yaml`, with one explicitly authored service and database hooks only when needed.
- `infra/`, copied from one extension-owned, parameterized Bicep template for
  embedded, MySQL/password, PostgreSQL/password, PostgreSQL/managed identity,
  and optional Foundry chat or embedding resources.
- `.azd-spring/`, containing database hooks when needed and, without an
  application-owned Dockerfile, generated container assets and Spring overrides.

When the application has a root `Dockerfile`, initialization preserves and uses
it instead of generating Docker assets. Deployment-owned Spring profiles, port,
datasource, and Foundry bindings are supplied through Container Apps environment
variables, including explicit application inputs and secret references for
password, API-key, or application-secret authentication. The extension does not
validate or rewrite the Dockerfile; it remains responsible for producing a runnable
HTTP image and including any application-owned external configuration it needs.
Without a root Dockerfile, the container builds through Maven
(using the wrapper when present), and loads the generated properties through
`SPRING_CONFIG_ADDITIONAL_LOCATION` from an extension-owned container directory.
A root `.dockerignore` without a root `Dockerfile` is rejected before Maven
inspection because the generated Dockerfile-specific ignore file would bypass
its exclusions.
Deployment image builds skip test compilation and execution.
The deployment binds Spring to all container interfaces.
Active conventional external configuration,
including profile-specific files, is retained in the runtime image. Password
bindings use template-owned Container Apps secret references; literal credentials
are not copied from source URLs.
Initialization rejects literal `spring.datasource.password` values and literal
passwords in MySQL/PostgreSQL JDBC user-info or `password` query parameters across
all conventional packaged application configuration, including inactive profiles,
and external application files copied into the runtime image. Empty values and
runtime placeholders without defaults are allowed.
JDBC URLs that cannot be safely parsed are rejected, including unresolved host
or port placeholders, even in inactive packaged profiles and retained external files.
For Java properties files, interactive initialization can offer to remove a
literal password or non-empty placeholder fallback while preserving the existing
placeholder name. Each newly discovered batch of packaged or retained external
properties requires separate confirmation of its sanitized file/property list;
credential values are never displayed. This source repair is unavailable with
`--no-prompt`, including when `--yes` is supplied. YAML and JDBC URL credentials
remain explicit application fixes.
This targeted validation does not scan arbitrary properties, application code, or
other packaged resources; review those before building or publishing an image.
Initialization also rejects literal `spring.ai.azure.openai.api-key` values in
the same packaged and retained external configuration scope. Interactive Java
properties repair follows the datasource-password confirmation and non-disclosure
rules.
Existing `azure.yaml`, `azure.yml`, `infra/`, or `.azd-spring/` files are not
adopted or overwritten. Root Dockerfiles and `.dockerignore` files are preserved.
Generated Dockerfile-specific ignore rules exclude exact development artifacts
without broad credential-like filename patterns.
Development Compose files are left alone.

**Development defaults:** password authentication uses generated administrator
credentials. Database public endpoints allow only ACA-reported outbound IPv4
addresses. PostgreSQL managed identity temporarily allows all public IPv4 addresses
while the local postprovision hook performs administrator bootstrap.
Local SQL access in password mode requires an explicitly added firewall rule.
JDBC connections require TLS. This is restricted public access, not private
connectivity or an application-exclusive network boundary.
ACA addresses can change; run `azd provision` to refresh the allowlist. Incremental
deployment does not remove obsolete rules, including any legacy
`development-public-access` rule from an older template; remove those explicitly.
There is no VNet integration, private endpoint, or fixed-egress configuration.
Foundry keeps public network access enabled with a default-deny network ACL that
allows only the same ACA-reported outbound addresses. API-key mode resolves the
account key inside Bicep and injects it directly into a Container Apps secret;
the key is not stored in generated files or azd environment values. Managed
identity mode disables local authentication, grants the Container App identity
`Cognitive Services OpenAI User`, and creates no Foundry secret.
Stronger JDBC certificate-verification modes are preserved, using the image's
system CA bundle for PostgreSQL and Java trust store for MySQL.
Embedded databases are not a durable Azure storage solution.
Memory mode needs no database inputs for non-interactive provision or deploy.
Resolved explicit in-memory URLs are retained in deployment-only configuration.
Embedded driver conflicts and unsupported URL options are rejected. Supported
options are H2 `DB_CLOSE_DELAY=-1/0`, `DB_CLOSE_ON_EXIT=true/false`, and Derby
`create=true`; URL credentials and initialization commands are not supported.

Passwords are created once by the preprovision hook and retained in local `.azure`
state. Do not commit this state. No Key Vault credential copy is created.
Provisioning preserves the standard `SERVICE_<NAME>_IMAGE_NAME` deployment output.
ACA uses a system identity; ordered Bicep modules create a public placeholder,
grant registry-scoped `AcrPull`, and configure registry access. The bootstrap
uses `@onlyIfNotExists()` to avoid resetting later deployments.
The placeholder keeps port 80. Standard azd service-module deployment applies
the built image, inferred Spring port, and database bindings.
Runtime containers use explicit TCP Startup, Liveness, and Readiness probes on
that port. Startup provides approximately four minutes of initialization allowance
before liveness checks take effect; no Actuator endpoint is required.

Generated projects require Bicep 0.38.3+. Memory mode generates no database hooks
and needs neither PowerShell nor Python. Password mode uses a PowerShell 7
preprovision hook; Python is not required.
Only PostgreSQL managed identity generates the Python prerequisite/bootstrap
scripts and dependency file; it requires PowerShell 7, Python, Azure CLI, and
the Python packages below. These hooks run locally or on the CI runner, not
inside the deployed Spring application.

### PostgreSQL managed-identity prerequisites

Add `com.azure:azure-identity-extensions` as a compile/runtime dependency in the
trusted app (1.2.10 was used by the standalone experiment). Initialization does
not edit the POM. Set these values in the selected azd environment before provision:

```sh
python -m pip install -r .azd-spring/requirements.txt
azd env set SPRING_DATABASE_ADMIN_OID "<Entra administrator user object ID>"
azd env set SPRING_DATABASE_ADMIN_NAME "<administrator user principal name>"
azd env set SPRING_DATABASE_BOOTSTRAP_SQL "<path to reviewed application SQL>"
```

Authenticate Azure CLI with `az login` for the selected subscription; `azd auth login`
alone is insufficient. The preprovision hook requests and discards a PostgreSQL
access token before creating resources. This checks CLI authentication, not SQL permissions
or database connectivity. The postprovision hook connects as that administrator
using an in-memory Azure CLI token in the selected subscription and verified TLS. It maps the ACA object ID
to non-admin role `azd_app`, then executes the supplied SQL in the application
database. The script must initialize/migrate the schema as the administrator
and grant only required table/schema/sequence permissions to `azd_app`.
The hook owns the transaction: commands such as `BEGIN`, `COMMIT`, and `ROLLBACK`
are rejected, including commits inside called procedures.
SQL runs through PostgreSQL's atomic execution context so failed validation
rolls back schema/grant changes; comments, strings, and routine bodies are not
rejected merely for containing these words.
`CONNECT` is granted by the hook; public database privileges and public schema
creation are revoked. Runtime admin, DDL, TEMP, or object ownership fail validation.
Administrative flags, database/schema creation, temporary-table privileges, and
object ownership (including routines, types, and schemas, not just relations)
are checked on `azd_app` and all direct/transitive role
memberships, including roles reachable through `SET ROLE` when inheritance is disabled.
This is the extension's runtime policy, not an Entra authentication requirement.

Example grants after app-owned schema initialization (replace object names):

```sql
GRANT USAGE ON SCHEMA public TO azd_app;
GRANT SELECT, INSERT, UPDATE ON public.items TO azd_app;
GRANT USAGE ON SEQUENCE public.items_id_seq TO azd_app;
```

The runtime receives no password; the JDBC plugin explicitly selects managed
identity. Runtime schema initialization and Hibernate DDL are disabled. Application
migration tools must be configured separately rather than run under this role.
Unchanged bootstrap SQL is skipped using a local host/identity/script digest
that also includes database and runtime-role object IDs. Recreating either
invalidates cached grants, including after a failed recovery attempt.
Principal mapping and privilege checks still run. Changed SQL executes again:
make it an intentional, repeatable migration, not a destructive sample reset.

The hook deletes only its temporary full-IPv4 `bootstrap-local-ip` firewall rule
after SQL setup, on success or failure, and reports cleanup failures without
recording success.
ACA rules remain. The next `azd provision` recreates temporary bootstrap access.
Provisioning interrupted before the hook runs can leave broad public access; remove
the rule explicitly.
RBAC propagation may still delay registry access.
MySQL/managed identity needs separate server-identity/Graph
configuration and live validation; it is explicitly rejected.

The selected namespace-first command follows existing extension conventions and
the agreed `azd spring init` experience; it differs from the verb-first recommendation
in the [extension guidelines](../../docs/extensions/extensions-style-guide.md).

## Development checks

```sh
go test ./...
go vet ./...
go build
python internal/spring/database_bootstrap_test.py
```

CI also runs `internal/spring/database_privileges_test.py` against disposable
PostgreSQL 15. To run locally, install the bootstrap requirements and set
`SPRING_TEST_POSTGRES_DSN` to a disposable PostgreSQL 15 superuser connection string.
Privilege cases roll back their fixtures. Transaction-boundary cases use separate
connections to verify rejected changes were not committed, then remove their fixtures.

Documented decision fixtures are in
[`internal/spring/testdata/resolution`](internal/spring/testdata/resolution/README.md).
Foundry chat acceptance applications and reviewed generated output live in the
independent
[`azure-dev-spring-extension-tests`](https://github.com/weidongxu-microsoft/azure-dev-spring-extension-tests)
repository.

The extension imports a published azd Go module for the SDK/Maven tooling, but
does not call core project serialization or infrastructure composition APIs.
It does not require core changes or a local module replacement. Infrastructure
compilation, application packaging, and live Azure deployment are distinct stages.

### Petclinic acceptance input

Use upstream [Spring Petclinic](https://github.com/spring-projects/spring-petclinic/tree/500158f732419217507c7656904b8e6aa1bcc0d6),
pinned to `500158f732419217507c7656904b8e6aa1bcc0d6`, in a fresh checkout per case:
default H2, `--profiles mysql`, and `--profiles postgres`.
The application is not vendored; generic regression fixtures live in the Go tests.

The prior core-generated milestone passed all three cases through initialization,
Bicep compilation, Maven packaging, and real Spring property-resolution checks.

Live H2 and PostgreSQL container build/deployment, health, and database write/read
passed. Restarting the app reset H2 data and preserved PostgreSQL data as expected.
PostgreSQL used PostgreSQL 15 / Standard_B1ms in `westus3`, after a database-only
create/delete probe verified availability; the initial `eastus2` attempt encountered
a subscription region restriction. Test resource groups were deleted, and the
PostgreSQL test vault was purged.
That milestone's MySQL provisioning was blocked by the core scaffold's AVM 0.6.1
`dummyVault/dummyKey` reference. The owned template no longer uses that scaffold.
The migrated PostgreSQL/password configuration passed live deployment, form
write/read, and independent SQL verification with explicit startup protection.
Fresh H2 (`westus3`) and MySQL/password (`westus2`) runs also passed health,
form write/update/read, and restart checks using the shared template. H2 data
reset; MySQL data persisted and matched independent verified-TLS SQL reads.
The full four-mode live acceptance matrix remains incomplete. Standalone PostgreSQL
managed-identity and Bicep-only registry-staging experiments passed separately
and are evidence for the mechanisms, not proof of the integrated generator.
