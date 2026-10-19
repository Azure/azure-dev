# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Local prerequisites and SQL bootstrap for PostgreSQL managed identity.

Azure documents token authentication and non-admin Entra role mapping here:
https://learn.microsoft.com/azure/postgresql/security/security-entra-configure
https://learn.microsoft.com/azure/postgresql/security/security-manage-entra-users
The no-DDL/TEMP/ownership checks below are this extension's runtime policy,
not additional requirements imposed by managed-identity authentication.
"""

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import uuid


ROOT = Path(__file__).resolve().parents[1]
ROLE = "azd_app"


def command(tool, *arguments):
    executable = shutil.which(tool)
    if executable is None:
        raise RuntimeError(f"Required tool is not installed: {tool}")
    try:
        result = subprocess.run(
            [executable, *arguments], cwd=ROOT, shell=os.name == "nt",
            capture_output=True, text=True, timeout=120,
        )
    except subprocess.TimeoutExpired:
        raise RuntimeError(f"{tool} {arguments[0]} timed out; diagnose the operation locally") from None
    if result.returncode:
        # Captured output can contain credentials; report the operation, not its values.
        raise RuntimeError(f"{tool} {arguments[0]} failed; diagnose the operation locally")
    return result.stdout


def values():
    return json.loads(command("azd", "env", "get-values", "--output", "json"))


def set_value(key, value):
    command("azd", "env", "set", key, value)


def required(env, key):
    value = env.get(key, "").strip()
    if not value:
        raise RuntimeError(f"Set {key} in the azd environment before provisioning")
    return value


def bootstrap_sql(env):
    file = Path(required(env, "SPRING_DATABASE_BOOTSTRAP_SQL"))
    if not file.is_absolute():
        file = ROOT / file
    content = file.read_text(encoding="utf-8")
    if not content.strip():
        raise RuntimeError("SPRING_DATABASE_BOOTSTRAP_SQL must contain reviewed schema and runtime grants")
    return content


def bootstrap_digest(host, database, database_oid, role_oid, principal, script):
    state = "\n".join((host, database, str(database_oid), str(role_oid), principal, script))
    return hashlib.sha256(state.encode()).hexdigest()


def execute_bootstrap_sql(connection, script):
    import psycopg
    from psycopg import sql

    # Execute through PostgreSQL's atomic SPI context, not the top-level SQL protocol.
    # SPI rejects transaction commands (including nested procedure commits), so supplied SQL
    # cannot commit grants before validation. PostgreSQL parses comments/strings/function bodies;
    # no client-side SQL splitting or keyword matching is needed.
    # https://www.postgresql.org/docs/15/spi-spi-execute.html
    # Quote both the SQL string and the DO body; supplied dollar quotes cannot end our wrapper.
    body = sql.SQL("BEGIN EXECUTE {}; END").format(sql.Literal(script)).as_string(connection)
    try:
        connection.execute(sql.SQL("DO {}").format(sql.Literal(body)))
    except (psycopg.errors.FeatureNotSupported, psycopg.errors.InvalidTransactionTermination) as error:
        if error.sqlstate == "2D000" or error.diag.message_primary == "EXECUTE of transaction commands is not implemented":
            raise RuntimeError(
                "Bootstrap SQL cannot control transactions; remove transaction commands such as "
                "BEGIN, COMMIT, ROLLBACK, and SAVEPOINT, including inside called procedures"
            ) from None
        raise


def validate_runtime_privileges(connection):
    # MEMBER includes direct/transitive SET ROLE targets, even through NOINHERIT.
    # USAGE or checking only the login role would miss privileges acquired by switching roles.
    # https://www.postgresql.org/docs/15/role-membership.html
    # https://www.postgresql.org/docs/15/functions-info.html#FUNCTIONS-INFO-ACCESS-TABLE
    flags = connection.execute(
        "SELECT bool_or(rolsuper), bool_or(rolcreatedb), bool_or(rolcreaterole) FROM pg_roles "
        "WHERE rolname = %s OR pg_has_role(%s, oid, 'MEMBER')", (ROLE, ROLE),
    ).fetchone()
    if flags != (False, False, False):
        raise RuntimeError("Runtime SQL role must not have administrative privileges")
    # Ownership permits ALTER/DROP even after CREATE is revoked. Shared owner dependencies
    # cover routines, types, schemas, and other objects without an incomplete catalog list.
    # Scope them to this database and shared objects; retain pg_class for temporary relations.
    # https://www.postgresql.org/docs/15/catalog-pg-shdepend.html
    unsafe = connection.execute(
        "WITH runtime_roles AS ("
        "SELECT oid FROM pg_roles WHERE rolname = %s OR pg_has_role(%s, oid, 'MEMBER')) "
        "SELECT bool_or(pg_has_role(oid, 'azure_pg_admin', 'MEMBER')), "
        "bool_or(has_database_privilege(oid, current_database(), 'CREATE')), "
        "bool_or(has_database_privilege(oid, current_database(), 'TEMP')), "
        "bool_or(EXISTS (SELECT 1 FROM pg_namespace "
        "WHERE has_schema_privilege(runtime_roles.oid, pg_namespace.oid, 'CREATE') "
        "AND nspname !~ '^pg_(toast_)?temp_[0-9]+$')), "
        "bool_or(EXISTS (SELECT 1 FROM pg_class WHERE relowner = runtime_roles.oid) "
        "OR EXISTS (SELECT 1 FROM pg_shdepend WHERE refclassid = 'pg_authid'::regclass "
        "AND refobjid = runtime_roles.oid AND deptype = 'o' "
        "AND dbid IN (0, (SELECT oid FROM pg_database WHERE datname = current_database())))) "
        "FROM runtime_roles",
        (ROLE, ROLE),
    ).fetchone()
    if any(unsafe):
        raise RuntimeError("Runtime SQL role must not have admin, DDL, TEMP, or object ownership")


def prepare(settings, env):
    if settings["engine"] != "postgresql" or settings["auth"] != "managedIdentity":
        raise RuntimeError("Unsupported database/authentication combination")
    uuid.UUID(required(env, "SPRING_DATABASE_ADMIN_OID"))
    required(env, "SPRING_DATABASE_ADMIN_NAME")
    bootstrap_sql(env)
    for package in ("psycopg", "certifi"):
        if importlib.util.find_spec(package) is None:
            raise RuntimeError("Install bootstrap dependencies: python -m pip install -r .azd-spring/requirements.txt")
    if env.get("SPRING_DATABASE_PASSWORD"):
        raise RuntimeError("Remove SPRING_DATABASE_PASSWORD before provisioning managed-identity authentication")
    subscription = required(env, "AZURE_SUBSCRIPTION_ID")
    # Token acquisition checks CLI sign-in, not SQL permissions or database connectivity.
    try:
        command(
            "az", "account", "get-access-token", "--resource-type", "oss-rdbms",
            "--subscription", subscription, "--output", "none",
        )
    except RuntimeError as error:
        raise RuntimeError(
            "Azure CLI token preflight failed; ensure Azure CLI is installed and run az login "
            "for the selected subscription"
        ) from error
    print("Managed-identity prerequisites checked; schema/grant SQL executes as the Entra administrator.")


def bootstrap(settings, env):
    if settings["engine"] != "postgresql" or settings["auth"] != "managedIdentity":
        raise RuntimeError("SQL identity bootstrap requires PostgreSQL managed identity")
    subscription = required(env, "AZURE_SUBSCRIPTION_ID")
    group = required(env, "AZURE_RESOURCE_GROUP")
    server = required(env, "SPRING_DATABASE_SERVER_NAME")
    try:
        digest = bootstrap_database(settings, env)
    finally:
        # Bicep opens this dedicated rule on each provision. Remove only that rule, even if
        # SQL/authentication fails; the separate ACA egress rules must remain untouched.
        # https://learn.microsoft.com/rest/api/postgresql/firewall-rules/delete
        try:
            command(
                "az", "postgres", "flexible-server", "firewall-rule", "delete",
                "--subscription", subscription, "--resource-group", group, "--server-name", server,
                "--name", "bootstrap-local-ip", "--yes",
            )
        except RuntimeError as error:
            raise RuntimeError(
                "Failed to remove temporary PostgreSQL firewall rule bootstrap-local-ip; "
                "remove it manually from the provisioned server before retrying azd provision"
            ) from error
    # Do not record/report success until both SQL validation and firewall cleanup succeed.
    set_value("SPRING_DATABASE_BOOTSTRAP_DIGEST", digest)
    print("Non-admin SQL identity verified; reviewed schema/grants applied or unchanged bootstrap skipped.")


def bootstrap_database(settings, env):
    import certifi
    import psycopg
    from psycopg import sql

    principal = str(uuid.UUID(required(env, "SPRING_APPLICATION_PRINCIPAL_ID")))
    subscription = required(env, "AZURE_SUBSCRIPTION_ID")
    app = json.loads(command(
        "az", "containerapp", "show", "--ids", required(env, "SPRING_APPLICATION_ID"),
        "--subscription", subscription, "-o", "json",
    ))
    if app["identity"]["type"] != "SystemAssigned" or app["identity"]["principalId"].lower() != principal:
        raise RuntimeError("Provisioned application identity does not match the SQL bootstrap identity")
    if not app["properties"].get("outboundIpAddresses"):
        raise RuntimeError("ACA has no reported outbound IPs; database connectivity is not established")
    script = bootstrap_sql(env)
    host = required(env, "SPRING_DATABASE_HOST")
    database = settings["database"]
    token = json.loads(command(
        "az", "account", "get-access-token", "--resource-type", "oss-rdbms",
        "--subscription", subscription, "-o", "json",
    ))
    account = json.loads(command("az", "account", "show", "--subscription", subscription, "-o", "json"))
    if token["tenant"] != account["tenantId"]:
        raise RuntimeError("Azure CLI token tenant does not match the selected account")
    credentials = {"password": token["accessToken"]}

    def connect(database):
        return psycopg.connect(
            host=host, dbname=database, user=required(env, "SPRING_DATABASE_ADMIN_NAME"),
            sslmode="verify-full", sslrootcert=certifi.where(), connect_timeout=20,
            **credentials,
        )

    # Azure's principal functions run in postgres; the object ID pins the application identity.
    # isAdmin=false creates a regular role, but does not constrain grants in the supplied SQL.
    with connect("postgres") as connection:
        mapping = connection.execute(
            "SELECT objectid, isadmin FROM pg_catalog.pgaadauth_list_principals(false) WHERE rolname = %s",
            (ROLE,),
        ).fetchone()
        if mapping is None:
            connection.execute(
                "SELECT pg_catalog.pgaadauth_create_principal_with_oid(%s, %s, 'service', false, false)",
                (ROLE, principal),
            )
        elif str(mapping[0]).lower() != principal or mapping[1] != 0:
            raise RuntimeError("Existing SQL role has a different identity or administrator mapping")

    with connect(database) as connection:
        database_row = connection.execute(
            "SELECT oid FROM pg_database WHERE datname = current_database()",
        ).fetchone()
        if database_row is None:
            raise RuntimeError("Connected PostgreSQL database is missing from the system catalog")
        role_row = connection.execute("SELECT oid FROM pg_roles WHERE rolname = %s", (ROLE,)).fetchone()
        if role_row is None:
            raise RuntimeError("Runtime SQL role is missing after identity mapping")
        # Role creation commits separately. Its OID must invalidate cached grants even if
        # a failed schema transaction is retried after the replacement mapping already exists.
        digest = bootstrap_digest(host, database, database_row[0], role_row[0], principal, script)
        # Cache only SQL execution; identity and effective privilege checks must run on every provision.
        if env.get("SPRING_DATABASE_BOOTSTRAP_DIGEST") != digest:
            connection.execute(sql.SQL("REVOKE ALL ON DATABASE {} FROM PUBLIC").format(
                sql.Identifier(database)))
            connection.execute("REVOKE CREATE ON SCHEMA public FROM PUBLIC")
            execute_bootstrap_sql(connection, script)
        # A validation failure rolls back this schema/grant transaction without recording success.
        validate_runtime_privileges(connection)
        connection.execute(
            sql.SQL("GRANT CONNECT ON DATABASE {} TO {}").format(
                sql.Identifier(database), sql.Identifier(ROLE)))
    return digest


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("prepare", "bootstrap"))
    args = parser.parse_args()
    settings = json.loads((ROOT / ".azd-spring" / "database.json").read_text())
    (prepare if args.action == "prepare" else bootstrap)(settings, values())
