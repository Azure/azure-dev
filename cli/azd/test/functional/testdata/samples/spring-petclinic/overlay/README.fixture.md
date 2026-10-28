# Spring Petclinic final overlay

This directory contains only the azd-owned files for the unpublished
`azure.springboot` development scenario. It does not contain Petclinic source.

Copy these files into an empty working directory, install the locally built
`azure.springboot` extension bundle, and then run:

```text
AZD_ALLOW_NON_EMPTY_FOLDER=1 azd init \
  -t https://github.com/spring-projects/spring-petclinic \
  . -e <environment> --no-prompt
```

`AZD_ALLOW_NON_EMPTY_FOLDER` must be scoped to that child process. The exact URL
currently resolves to commit `500158f732419217507c7656904b8e6aa1bcc0d6`;
the functional test fails when the source manifest drifts.

Set `SPRING_DATABASE_TYPE` to `h2` or `postgresql`. A new PostgreSQL environment
accepts `SPRING_DATABASE_PASSWORD` from the process or generates and persists a
valid password. Existing environments require their original persisted
password. A local Docker-compatible engine is required to build the image.

This demo uses the PostgreSQL administrator login, public networking with exact
observed Container Apps outbound-IP rules, and local azd secret state. It is not
a production security architecture. If ACA outbound addresses change during a
revision replacement, retry `azd deploy`; predeploy reconciliation observes the
new set before the retry.

Live Azure tests are opt-in and must not run without explicit authorization.
