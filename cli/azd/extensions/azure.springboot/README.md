# Azure Spring Boot extension

`azure.springboot` packages and deploys a Maven-based Spring Boot app to Azure
Container Apps. The template owns Bicep; the extension reuses azd's container
build and publish services, then incrementally deploys the authored service
module. It generates a non-root Java 17 runtime Dockerfile and handles
PostgreSQL password preparation and firewall rules without template hooks.

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

This unpublished prototype requires azd 1.37.2 or newer, a locally built and
installed extension bundle, Java 17/Maven Wrapper, Docker, and the Bicep CLI.
azd can install the Bicep CLI when it is missing. The host is not available
from a public extension registry.

## Current command limits

The supported command shape uses separate `azd init`, `up`, `package`,
`deploy`/redeploy, and `down` operations. `azd init -t` leaves a newly
initialized Petclinic repository without a Git commit or `HEAD`. For that
specific Spring package invocation, the extension reuses azd's native Maven
framework while disabling the optional Git metadata plugin; normal clones and
non-Spring Java services retain native Maven behavior. Custom nested command
workflows and standalone hidden `azd build` flows are outside the supported
scope.

H2 is the default. PostgreSQL/password is demo-only: administrator login,
local azd secret state, and public networking restricted to observed ACA egress
addresses. Supply `SPRING_DATABASE_PASSWORD` before first provision or let the
extension generate it. Reruns reuse it; lost state requires the original
password, not automatic rotation. Changing database mode requires a new
environment. No Key Vault, Gradle, project scanning, or Bicep generation.

The [Petclinic testing template](https://github.com/XiaofeiCao/spring-petclinic/blob/azd-springboot-test/README.azd.md)
owns setup, demo, recovery, cleanup, and opt-in E2E instructions.
