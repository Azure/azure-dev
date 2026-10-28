# Azure Spring Boot extension

`azure.springboot` is an unpublished first-party development extension that
prepares Maven-based Spring Boot applications for deployment through azd's
built-in Azure Container Apps target.

The extension is intentionally narrow:

- Java 17 and a Maven Wrapper are required.
- A deterministic, non-root multi-stage Dockerfile is created during
  `prebuild`.
- `config.database.type` supports `h2` and `postgresql`.
- PostgreSQL uses password authentication and the server administrator login.
- PostgreSQL firewall rules match the Container App's observed outbound IPv4
  addresses.

It does not generate application source or Bicep, scan arbitrary projects,
support Gradle, rotate credentials, or replace the native ACA deployment
pipeline.

## Service configuration

```yaml
services:
  app:
    project: .
    language: java
    host: azure.springboot
    module: app
    config:
      database:
        type: ${SPRING_DATABASE_TYPE:-h2}
```

Only `config.database.type` is accepted. Missing configuration defaults to
`h2`; explicit empty, unknown, wrong-type, or unsupported fields fail before
the extension writes files or environment state.

## Environment values

| Name | Direction | Description |
|---|---|---|
| `SPRING_DATABASE_TYPE` | Input/state | `h2` or `postgresql`; missing defaults to H2. |
| `SPRING_DATABASE_PASSWORD` | Secret input/state | Optional for a new PostgreSQL environment; generated when absent and persisted locally by azd. Never valid for H2. |
| `SPRING_DATABASE_PROVISIONED_TYPE` | Extension state | Binds a provisioned environment to its database mode. |
| `AZURE_CONTAINER_APP_ID` | Bicep output | Authoritative Container App identity used for outbound-IP lookup. |
| `AZURE_POSTGRES_SERVER_ID` | Bicep output | Authoritative PostgreSQL Flexible Server identity. |
| `AZURE_POSTGRES_SERVER_NAME` | Bicep output | Cross-check for the persisted server identity. |

A persisted password wins on reruns. A conflicting process value fails.
Existing PostgreSQL infrastructure without its original local password fails
closed; automatic password rotation is not supported. Generation occurs only
after read-only Azure checks find neither an azd deployment nor a tagged
PostgreSQL server for the selected environment. Lookup or ownership failures
also fail closed; supply the original password to restore lost local state.

## Development fixture

The authored fixture is at
`cli/azd/test/functional/testdata/samples/spring-petclinic`. It stages azd
configuration before running:

```text
azd init -t https://github.com/spring-projects/spring-petclinic . -e <environment>
```

The bare upstream repository is not an azd template and does not contain these
assets. The fixture records and validates expected upstream commit
`500158f732419217507c7656904b8e6aa1bcc0d6`.

The selected build path requires a local Docker-compatible engine. PostgreSQL
uses public networking, administrator credentials, local azd secret state, and
exact dynamic egress rules. These are demo trade-offs, not production
recommendations. If outbound IPs change during replacement, retry
`azd deploy` so predeploy reconciliation can repair the rules. PostgreSQL
readiness includes database health, while liveness remains database-independent.

Live Azure tests, including fresh-runner password restoration and cleanup
verification, are opt-in and require separate authorization. They are not run
by the local test suite.
