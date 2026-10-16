# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Local prerequisites and SQL bootstrap for PostgreSQL managed identity."""

import argparse
import hashlib
import importlib.util
import ipaddress
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


def prepare(settings, env):
    if settings["engine"] != "postgresql" or settings["auth"] != "managedIdentity":
        raise RuntimeError("Unsupported database/authentication combination")
    uuid.UUID(required(env, "SPRING_DATABASE_ADMIN_OID"))
    required(env, "SPRING_DATABASE_ADMIN_NAME")
    address = ipaddress.IPv4Address(required(env, "SPRING_DATABASE_BOOTSTRAP_IP"))
    if address.is_unspecified:
        raise RuntimeError("Bootstrap firewall IP must not be 0.0.0.0")
    bootstrap_sql(env)
    for package in ("psycopg", "certifi"):
        if importlib.util.find_spec(package) is None:
            raise RuntimeError("Install bootstrap dependencies: python -m pip install -r .azd-spring/requirements.txt")
    if env.get("SPRING_DATABASE_PASSWORD"):
        raise RuntimeError("Remove SPRING_DATABASE_PASSWORD before provisioning managed-identity authentication")
    subscription = required(env, "AZURE_SUBSCRIPTION_ID")
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
    digest = hashlib.sha256((host + "\n" + principal + "\n" + script).encode()).hexdigest()
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

    with connect(settings["database"]) as connection:
        if env.get("SPRING_DATABASE_BOOTSTRAP_DIGEST") != digest:
            connection.execute(sql.SQL("REVOKE ALL ON DATABASE {} FROM PUBLIC").format(
                sql.Identifier(settings["database"])))
            connection.execute("REVOKE CREATE ON SCHEMA public FROM PUBLIC")
            connection.execute(script, prepare=False)
        flags = connection.execute(
            "SELECT rolsuper, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = %s", (ROLE,),
        ).fetchone()
        if flags != (False, False, False):
            raise RuntimeError("Runtime SQL role must not have administrative privileges")
        unsafe = connection.execute(
            "SELECT pg_has_role(%s, 'azure_pg_admin', 'MEMBER'), "
            "has_database_privilege(%s, current_database(), 'CREATE'), "
            "has_database_privilege(%s, current_database(), 'TEMP'), "
            "EXISTS (SELECT 1 FROM pg_namespace WHERE has_schema_privilege(%s, oid, 'CREATE') "
            "AND nspname NOT LIKE 'pg_temp_%%' AND nspname NOT LIKE 'pg_toast_temp_%%'), "
            "EXISTS (SELECT 1 FROM pg_class WHERE relowner = (SELECT oid FROM pg_roles WHERE rolname = %s)), "
            "EXISTS (SELECT 1 FROM pg_class WHERE relowner <> (SELECT oid FROM pg_roles WHERE rolname = %s) "
            "AND pg_has_role((SELECT oid FROM pg_roles WHERE rolname = %s), relowner, 'MEMBER'))",
            (ROLE, ROLE, ROLE, ROLE, ROLE, ROLE, ROLE),
        ).fetchone()
        if any(unsafe):
            raise RuntimeError("Runtime SQL role must not have admin, DDL, TEMP, or object ownership")
        connection.execute(
            sql.SQL("GRANT CONNECT ON DATABASE {} TO {}").format(
                sql.Identifier(settings["database"]), sql.Identifier(ROLE)))
    set_value("SPRING_DATABASE_BOOTSTRAP_DIGEST", digest)
    print("Non-admin SQL identity verified; reviewed schema/grants applied or unchanged bootstrap skipped.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("prepare", "bootstrap"))
    args = parser.parse_args()
    settings = json.loads((ROOT / ".azd-spring" / "database.json").read_text())
    (prepare if args.action == "prepare" else bootstrap)(settings, values())
