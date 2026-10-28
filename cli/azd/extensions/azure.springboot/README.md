# Azure Spring Boot extension

`azure.springboot` prepares a Maven-based Spring Boot app; azd's native Azure
Container Apps target builds and deploys it. The template owns Bicep. The
extension generates a non-root Dockerfile before build and handles PostgreSQL
password preparation and firewall rules, without template hooks.

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

This unpublished prototype requires the matching development azd build,
a locally installed extension bundle, Java 17/Maven Wrapper, and Docker.
Released azd alone does not provide this host.

H2 is the default. PostgreSQL/password is demo-only: administrator login,
local azd secret state, and public networking restricted to observed ACA egress
addresses. Supply `SPRING_DATABASE_PASSWORD` before first provision or let the
extension generate it. Reruns reuse it; lost state requires the original
password, not automatic rotation. Changing database mode requires a new
environment. No Key Vault, Gradle, project scanning, or Bicep generation.

The [Petclinic testing template](https://github.com/XiaofeiCao/spring-petclinic/blob/azd-springboot-test/README.azd.md)
owns setup, demo, recovery, cleanup, and opt-in E2E instructions.
