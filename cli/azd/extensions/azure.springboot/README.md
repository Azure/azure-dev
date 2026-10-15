# Spring Boot

Development extension for `azd spring init`. It infers a Spring Boot application's
active database and generates an azd project without requiring an existing `azure.yaml`.
It is not yet published to the extension registry.

## Try it locally

From this extension directory, build and register a local bundle:

```sh
azd x build
azd x pack --bundle --output ./bin/bundle
azd extension install ./bin/bundle/azure-springboot_0.0.1.zip --force
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

Use Spring's normal configuration to activate alternatives, for example
`SPRING_PROFILES_ACTIVE`. There is no extension-specific profile selector.

Use `--database-auth managedIdentity` to select PostgreSQL token authentication
explicitly, or let initialization infer supported passwordless Spring properties
or the Azure PostgreSQL JDBC authentication plugin. An explicit password choice
cannot downgrade detected passwordless intent. The flag is rejected without an
external datasource. MySQL managed identity is deferred.

## Initial scope

Single-module Maven applications using Spring Boot executable JAR packaging and
Java 17 or newer. Analysis reads effective Maven metadata, conventional
`application*.properties`/YAML configuration, active/default profiles,
includes/groups, property placeholders, environment overrides, and
`SPRING_APPLICATION_JSON`. Inactive database alternatives are not provisioned.
JDBC dependencies establish available drivers, not active database requirements.
Repackaging checks include plugin defaults and active execution configuration.
The resolved Maven build directory determines the JAR copy path; project-local
custom output directories, including spaces and inherited settings, are supported.
Missing/unresolved paths, directories outside the Docker context, and output
directories overlapping application inputs or generated files are rejected.
Custom Hikari username/password bindings, including empty values, are rejected;
use `spring.datasource.username/password` instead. Ordinary Hikari pooling is supported.
The normalized Maven artifact ID must start with a letter, contain no consecutive
hyphens, and be at most 28 characters so the derived `<name>-app` fits Azure naming rules.

| Active datasource | Generated database |
| --- | --- |
| In-memory H2, Derby, or HSQLDB | None |
| MySQL JDBC URL | Azure Database for MySQL server and the inferred database |
| PostgreSQL JDBC URL | Azure Database for PostgreSQL server and the inferred database |

Hosting uses Azure Container Apps, a container registry, and Log Analytics.
Gradle, multi-module/custom packaging, custom datasource beans, programmatic
profile activation, custom configuration locations/imports, and other resource
types require further analysis and are explicitly rejected when detected.
File-based embedded databases require a persistence policy and are not adopted.

## Generated files and boundaries

- `azure.yaml`, with one explicitly authored service and standard azd hooks.
- `infra/`, copied from one extension-owned, parameterized Bicep template for
  embedded, MySQL/password, PostgreSQL/password, and PostgreSQL/managed identity.
- `.azd-spring/`, containing a Java-version-aware Dockerfile, Dockerfile-specific
  ignore rules, and deployment-only Spring datasource overrides.

The application source remains unchanged. The container builds through Maven
(using the wrapper when present), and loads the generated properties through
`SPRING_CONFIG_ADDITIONAL_LOCATION`. Active conventional external configuration,
including profile-specific files, is retained in the runtime image. Password
bindings use template-owned Container Apps secret references; literal credentials
are not copied from source URLs.
Existing configuration files and packaged resources may still contain credentials;
review them before building or publishing an image.
Existing `azure.yaml`, `infra/`, `.azd-spring/`, or root Dockerfiles are not adopted
or overwritten. Development Compose files are left alone.

**Development defaults:** SQL authentication uses generated administrator
credentials and the template's firewall rules allow all public IP addresses.
JDBC connections require TLS. This is not a restrictive production network policy.
Embedded databases are not a durable Azure storage solution.
Memory mode needs no database inputs for non-interactive provision or deploy.

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

Generated projects require PowerShell, Python, and Bicep 0.38.3+.
PostgreSQL managed identity also requires Azure CLI and the Python packages below.

### PostgreSQL managed-identity prerequisites

Add `com.azure:azure-identity-extensions` as a compile/runtime dependency in the
trusted app (1.2.10 was used by the standalone experiment). Initialization does
not edit the POM. Set these values in the selected azd environment before provision:

```sh
python -m pip install -r .azd-spring/requirements.txt
azd env set SPRING_DATABASE_ADMIN_OID "<Entra administrator user object ID>"
azd env set SPRING_DATABASE_ADMIN_NAME "<administrator user principal name>"
azd env set SPRING_DATABASE_BOOTSTRAP_IP "<bootstrap public IPv4>"
azd env set SPRING_DATABASE_BOOTSTRAP_SQL "<path to reviewed application SQL>"
```

The preprovision hook checks prerequisites before creating resources. The
postprovision hook connects as that administrator using an in-memory Azure CLI
token in the selected subscription and verified TLS. It maps the ACA object ID
to non-admin role `azd_app`, then executes the supplied SQL in the application
database. The script must initialize/migrate the schema as the administrator
and grant only required table/schema/sequence permissions to `azd_app`.
`CONNECT` is granted by the hook; public database privileges and public schema
creation are revoked. Runtime admin, DDL, TEMP, or object ownership fail validation.

Example grants after app-owned schema initialization (replace object names):

```sql
GRANT USAGE ON SCHEMA public TO azd_app;
GRANT SELECT, INSERT, UPDATE ON public.items TO azd_app;
GRANT USAGE ON SEQUENCE public.items_id_seq TO azd_app;
```

The runtime receives no password; the JDBC plugin explicitly selects managed
identity. Runtime schema initialization and Hibernate DDL are disabled. Application
migration tools must be configured separately rather than run under this role.
Unchanged bootstrap SQL is skipped using a local host/identity/script digest,
but principal mapping and privilege checks still run. Changed SQL executes again:
make it an intentional, repeatable migration, not a destructive sample reset.

The template declares exact bootstrap and ACA-reported IPv4 firewall rules for
this path. ACA addresses can change; obsolete rules are not reconciled. This is
not production fixed egress/private networking. RBAC propagation may still delay
registry access. MySQL/managed identity needs separate server-identity/Graph
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

The extension imports a published azd Go module for the SDK/Maven tooling, but
does not call core project serialization or infrastructure composition APIs.
It does not require core changes or a local module replacement. Infrastructure
compilation, application packaging, and live Azure deployment are distinct stages.

### Petclinic acceptance input

Use upstream [Spring Petclinic](https://github.com/spring-projects/spring-petclinic/tree/500158f732419217507c7656904b8e6aa1bcc0d6),
pinned to `500158f732419217507c7656904b8e6aa1bcc0d6`, in a fresh checkout per case:
default H2, `SPRING_PROFILES_ACTIVE=mysql`, and `SPRING_PROFILES_ACTIVE=postgres`.
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
