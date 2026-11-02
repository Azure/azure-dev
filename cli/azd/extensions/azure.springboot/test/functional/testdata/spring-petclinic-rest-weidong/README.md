# Pinned Weidong Petclinic REST overlay

Source: [weidongxu-microsoft/petclinic-rest-azd](https://github.com/weidongxu-microsoft/petclinic-rest-azd/tree/d8f601e5d3cdbd640069ffde58fc1370541fb0f4),
commit `d8f601e5d3cdbd640069ffde58fc1370541fb0f4`.

Only `azure.yaml` is overlaid. `upstream/` contains a small, byte-exact subset
for offline structural assertions, **not** a runnable or vendored REST app.
`source-revision.json` pins every upstream file's Git blob SHA-1 and names the
evidence subset. Upstream code is Apache 2.0; see `upstream/LICENSE.txt`
and the retained attribution in `upstream/NOTICE`.

To materialize the complete application explicitly (network download only,
no build, scripts, authentication to Azure or deployment):

```powershell
.\materialize.ps1 -Destination .\materialized-petclinic-rest
```

Use a new/empty destination. The script downloads the pinned archive, verifies
the complete original file inventory and blob hashes, then replaces only
`azure.yaml`. The overlay requires azd >=1.37.2 and a matching **unpublished**
local `azure.springboot` build, not a publicly available release.
If the GitHub archive applies `.gitattributes` export line endings, the
materializer retrieves the pinned raw file and rechecks its exact blob hash;
it never accepts a normalized substitute for the original bytes.

## Preserved contracts

- Service `spring-petclinic-rest-app`, module `application`, Bicep `infra/main`.
- The overlay omits `docker` and `database`: host Maven builds the JAR and the
  extension builds an isolated generated linux/amd64 runtime image. The authored
  Dockerfile and `.dockerignore` remain unchanged and unused by that route.
- Original `DATABASE_ADMIN_OBJECT_ID`/`DATABASE_ADMIN_NAME` inputs and
  `DATABASE_SERVER_ID`, `DATABASE_HOST`, `APPLICATION_IDENTITY_ID`,
  `APPLICATION_IDENTITY_CLIENT_ID`, `APPLICATION_PRINCIPAL_ID`,
  `CONTAINER_APP_ENVIRONMENT_ID` outputs; no authored aliases or extra tags.
- Original nested `parameters.<name>.value` substitutions and
  `SERVICE_SPRING_PETCLINIC_REST_APP_IMAGE_NAME` binding.
- Original port 9966, `/petclinic` endpoint, profiles and renewable MI JDBC
  authentication with hostname-verified TLS.
- No Prepare/Configure YAML hooks. Their script sources, infrastructure,
  parameter files, Dockerfile, `.dockerignore` and app sources remain unchanged.

## Discovered database and temporary networking

The unchanged application properties select PostgreSQL and petclinic_app,
Hikari's Azure identity plugin, the deployed AZURE_CLIENT_ID and verify-full
TLS. Runtime PostgreSQL and azure-identity-extensions dependencies provide
integration evidence; H2 is test-only and cannot establish production H2.
The fixed `app-owned-v2` policy gives the
app CONNECT and public-schema USAGE/CREATE plus applicable table/sequence
read/write. It creates and owns its schema objects and can ALTER/DROP those
objects, without database CREATE/TEMPORARY, administrator access or database
ownership. Existing admin/foreign application objects require fresh resources
or explicit migration, not automatic adoption.

Spring's original `spring.sql.init.mode=always` executes schema/seed SQL on
startup; Hibernate DDL remains `none`. There is no initializer receipt or
separate gate and no claim that restart SQL is perfectly idempotent.

**Temporary exposure:** unchanged `infra/database.bicep` creates
`bootstrap-local-ip` at `0.0.0.0`–`255.255.255.255`. Automatic extension setup
replaces the removed YAML hooks' cleanup responsibility: verify ownership,
delete only that broad rule and verify absence on success/failure/cancellation,
while retaining verified exact runtime-egress rules. Entra-only authentication
and verified TLS do not limit source IPs. Process-local cleanup is not an
expiring rule or a guarantee against host/credential loss.

Offline tests verify structure, hashes and REST JSON request/response semantics
using an in-process server. They do not prove a running application, actual SQL
ownership, image build or Azure acceptance. Live validation is separately gated.

## Published user fork

The minimal YAML-only change is published on
[`XiaofeiCao/petclinic-rest-azd`, branch `springbootapp-template`](https://github.com/XiaofeiCao/petclinic-rest-azd/blob/springbootapp-template/azure.yaml).
Commit:
[`ce7d05f5d2044ce91eedd8c8679d83386b757b39`](https://github.com/XiaofeiCao/petclinic-rest-azd/commit/ce7d05f5d2044ce91eedd8c8679d83386b757b39).
The fork's default branch remains untouched; no upstream PR was opened.
Remote tree verification found exactly one changed file, `azure.yaml`, with
all other tracked files retaining identical Git blob IDs. The published YAML
omits both `docker` and `database`, selecting native packaging and datasource
discovery. Live Azure acceptance remains separately gated.

## Historical local authored-Dockerfile validation (previous revision)

On 2026-11-02, Docker Engine 29.9.2 successfully built the unmodified pinned
Dockerfile from a freshly materialized, hash-verified source tree:

```shell
docker build --file Dockerfile --platform linux/amd64 --tag azd-spring-rest-check:767564a4-9ef24269 --progress plain .
```

The local image was Linux/amd64, exposed `9966/tcp`, ran as `10001:10001`,
and contained the Maven-built `spring-petclinic-rest-4.0.2.jar`.
An exact session-owned container with `--network none` started the original
default H2/Spring Data JPA profiles. A container-loopback request to
`GET /petclinic/api/owners` returned HTTP 200 JSON with seeded owners.
This tests the authored image and REST route, not PostgreSQL/managed identity,
Azure deployment, or azd's composite-framework artifact reuse.

After building, all 176 source files were rechecked: only the approved YAML
overlay differed from pinned upstream. Dockerfile and `.dockerignore` bytes
were unchanged. The exact owned container, image tag and scratch directory
were removed and absence verified. No registry push or upstream source fix
was performed.

The focused offline invocation from the extension directory was:

```shell
go test ./test/functional -run '^(TestSpringPetclinicManagedIdentityFixtureIsPortable|TestSpringPetclinicRest.*)$' -count=1
go vet ./test/functional
```

Passing test names:

- `TestSpringPetclinicManagedIdentityFixtureIsPortable`
- `TestSpringPetclinicRestPinnedOverlay`
- `TestSpringPetclinicRestUnchangedSourceContracts`
- `TestSpringPetclinicRestCRUDUsesJSONAPI`

## Local PostgreSQL application/DDL composition

On 2026-11-02, a second bounded local check used the same pinned source and
Docker build cache with a fresh cached `postgres:16-alpine` container.
Both containers ran on a uniquely owned **internal Docker network**, with
PostgreSQL data on container-scoped tmpfs. The final run published **no host
ports**; real REST requests used the application's loopback port 9966.
An initial local harness probe found no published-port binding on the internal
network; it was cleaned before the successful container-loopback run.

The database was owned by `postgres`, and `public` by `pg_database_owner`.
The ordinary `petclinic_app` role had no memberships, superuser, CREATEDB,
CREATEROLE, replication or BYPASSRLS capability. PUBLIC database privileges
were revoked, including CREATE/TEMPORARY, and PUBLIC schema privileges were
revoked. The app received only database CONNECT and public-schema USAGE/CREATE.
Catalog queries verified CREATE/TEMPORARY remained unavailable to the app and
were not inherited from PUBLIC.

Only the local container environment changed:

```text
SPRING_PROFILES_ACTIVE=postgres,spring-data-jpa
SPRING_DATASOURCE_URL=jdbc:postgresql://database:5432/petclinic?sslmode=disable
SPRING_DATASOURCE_USERNAME=petclinic_app
SPRING_DATASOURCE_PASSWORD=<transient local test secret>
SPRING_JPA_HIBERNATE_DDL_AUTO=none
```

The authored `spring.sql.init.mode=always` was unchanged. No source, SQL,
Dockerfile or image-recipe fixes were made.

**Observed results:** startup created all 9 application tables, 7 sequences
and 17 indexes under `petclinic_app` ownership. Seed counts were 10 owners,
6 vets, 3 specialties, 6 types, 13 pets, 4 visits, 1 user, 3 roles and
5 vet-specialty rows. Real JSON owner CRUD returned POST 201, GET 200,
PUT 204, GET with the updated city, DELETE 204 and subsequent GET 404.

After creating a retained owner and editing a seeded owner's city, one
application-container restart succeeded. Both changes persisted and all row
counts stayed the same (11 owners after the retained insert). No existing
table, sequence or index was removed/replaced, and ownership remained with
the app. However, the original unnamed `CREATE INDEX` statements created
**seven additional indexes**, increasing the count from 17 to 24:
`vets_last_name_idx1`, `specialties_name_idx1`, `types_name_idx1`,
`owners_last_name_idx1`, `pets_name_idx1`, `pets_owner_id_idx1` and
`visits_pet_id_idx1`. This is direct evidence that startup SQL is **not
catalog-idempotent**; the check does not promise arbitrary restart/migration
safety or change the upstream SQL.

All 176 host-source files were reverified afterward; only the approved YAML
overlay differed. Exact owned app/database containers, internal network,
application image, transient credentials and scratch directory were removed
in `finally`, with absence verified. The cached PostgreSQL image and unrelated
Docker resources were left alone. There were no Azure operations or pushes.

**Acceptance boundary:** the transient password and `sslmode=disable` are
strictly local test settings on the isolated Docker network. This verifies
real Spring startup DDL, ownership, seed execution, REST CRUD and one restart
under the fixed ordinary-role privileges. It does **not** verify Entra login,
the renewable managed-identity JDBC plugin, hostname-verified TLS, Azure
deployment/provenance/firewall behavior, or azd composite-framework packaging.
It is not a production password fallback or a substitute for separately
authorized Azure managed-identity/TLS acceptance.
