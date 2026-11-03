# Spring Petclinic MVC managed-identity fixture

This retained Spring MVC fixture uses the unpublished local `azure.springboot`
build with azd >=1.37.2, host JDK 17+, the Maven Wrapper, Docker and Bicep.
It is a local `azd init -t <fixture-path>` input, not a live deployment claim.
For Weidong's separate REST application, use the
[pinned REST overlay](../spring-petclinic-rest-weidong/README.md).

## Application-owned database

The extension discovers the effective managed-identity datasource from the
Spring application and verified deployment. The verified developer user is the
PostgreSQL Entra administrator; the attached UAMI maps to the non-admin
`petclinic_app` login. Its fixed `app-owned-v2` policy grants
CONNECT, public-schema USAGE/CREATE, table read/write and sequence privileges
as applicable. No database CREATE/TEMPORARY or administrator membership is
granted. Objects created by the app belong to it, permitting ALTER/DROP of its
own objects. Existing admin/foreign application object owners are rejected;
there is no implicit ownership migration.

Spring runs the original schema and seed SQL at each startup
(`spring.sql.init.mode=always`), with Hibernate DDL `none`. This may reset or
reseed data; no perfect idempotence or persistence across restarts is promised.
There is no separate initializer, SQL manifest, receipt or completion gate.
Access readiness verifies identity/grants/connectivity, not application tables.

Authored service-specific configuration is not part of this fixture. Old
DML-only markers and provisioner-owned objects require fresh resources or a
separately approved migration.

The extension verifies the current deployment and live resources before
grants, uses hostname-verified TLS, and manages only owned narrow firewall
rules for this fixture. `POSTGRES_BOOTSTRAP_CLIENT_IP` is an optional exact
public IPv4 troubleshooting override. Unlike the REST demo, this fixture
does not intentionally keep a public all-IPv4 rule.

## Use

After installing the matching local extension, initialize with
`azd init -t <fixture-path> . -e petclinic-mi --no-prompt`.
`azd package` is local; `azd up`/`deploy`/`down` require separate Azure authorization.

## Source and license

Application source remains based on
[`spring-projects/spring-petclinic@500158f`](https://github.com/spring-projects/spring-petclinic/tree/500158f732419217507c7656904b8e6aa1bcc0d6).
[`source-revision.json`](source-revision.json) records LF-normalized upstream
hashes and intentional fixture overrides, including startup SQL initialization
and the managed-identity JDBC plugin. The POM preserves Git metadata when HEAD
exists and skips it for a fresh unborn repository. See
[`LICENSE.txt`](LICENSE.txt) for the Apache 2.0 license.
