# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Privilege regressions against a disposable PostgreSQL 15 server, without Azure."""

import json
import os
import unittest
from unittest.mock import MagicMock, patch

from database_bootstrap_test import bootstrap


class DatabasePrivilegeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        import psycopg

        dsn = os.environ.get("SPRING_TEST_POSTGRES_DSN")
        if not dsn:
            raise RuntimeError("Set SPRING_TEST_POSTGRES_DSN to a disposable PostgreSQL 15 superuser connection")
        cls.dsn = dsn
        cls.connection = psycopg.connect(dsn, autocommit=True, connect_timeout=10)
        cls.addClassCleanup(cls.connection.close)
        version = int(cls.connection.execute("SHOW server_version_num").fetchone()[0])
        if not 150000 <= version < 160000:
            raise RuntimeError("Privilege regressions require PostgreSQL 15, matching the generated server")

    def initialize_roles(self, depth):
        from psycopg import sql

        connection = self.connection
        connection.execute("CREATE ROLE azure_pg_admin NOLOGIN")
        connection.execute("CREATE ROLE azd_app NOLOGIN NOINHERIT")
        connection.execute("CREATE ROLE privilege_parent NOLOGIN NOINHERIT")
        connection.execute("CREATE SCHEMA privilege_test")
        database = connection.execute("SELECT current_database()").fetchone()[0]
        connection.execute(sql.SQL("REVOKE ALL ON DATABASE {} FROM PUBLIC").format(sql.Identifier(database)))
        connection.execute("REVOKE CREATE ON SCHEMA public FROM PUBLIC")
        if depth == 0:
            return "azd_app"
        if depth == 1:
            connection.execute("GRANT privilege_parent TO azd_app")
        else:
            connection.execute("CREATE ROLE privilege_middle NOLOGIN NOINHERIT")
            connection.execute("GRANT privilege_parent TO privilege_middle")
            connection.execute("GRANT privilege_middle TO azd_app")
        return "privilege_parent"

    def test_safe_direct_and_transitive_memberships(self):
        for depth in (0, 1, 2):
            with self.subTest(depth=depth), self.connection.transaction(force_rollback=True):
                self.initialize_roles(depth)
                self.connection.execute("GRANT USAGE ON SCHEMA privilege_test TO privilege_parent")
                bootstrap.validate_runtime_privileges(self.connection)

    def test_create_and_temp_cannot_hide_behind_noinherit(self):
        from psycopg import sql

        for depth in (0, 1, 2):
            for privilege in ("database CREATE", "database TEMP", "schema CREATE"):
                with self.subTest(depth=depth, privilege=privilege), \
                        self.connection.transaction(force_rollback=True):
                    target = self.initialize_roles(depth)
                    database = self.connection.execute("SELECT current_database()").fetchone()[0]
                    grants = {
                        "database CREATE": sql.SQL("GRANT CREATE ON DATABASE {} TO {}").format(
                            sql.Identifier(database), sql.Identifier(target)),
                        "database TEMP": sql.SQL("GRANT TEMP ON DATABASE {} TO {}").format(
                            sql.Identifier(database), sql.Identifier(target)),
                        "schema CREATE": sql.SQL("GRANT CREATE ON SCHEMA privilege_test TO {}").format(
                            sql.Identifier(target)),
                    }
                    self.connection.execute(grants[privilege])
                    inherited = self.connection.execute(
                        "SELECT has_database_privilege('azd_app', current_database(), 'CREATE'), "
                        "has_database_privilege('azd_app', current_database(), 'TEMP'), "
                        "has_schema_privilege('azd_app', 'privilege_test', 'CREATE')",
                    ).fetchone()
                    self.assertEqual(any(inherited), depth == 0)

                    # Prove SET ROLE permits the operation, then undo it so no relation ownership
                    # can cause a false-positive rejection in the production privilege check.
                    self.connection.execute("SAVEPOINT role_operation")
                    self.connection.execute("SET SESSION AUTHORIZATION azd_app")
                    self.connection.execute(sql.SQL("SET ROLE {}").format(sql.Identifier(target)))
                    operations = {
                        "database CREATE": "CREATE SCHEMA acquired_schema",
                        "database TEMP": "CREATE TEMP TABLE acquired_table (id integer)",
                        "schema CREATE": "CREATE TABLE privilege_test.acquired_table (id integer)",
                    }
                    self.connection.execute(operations[privilege])
                    self.connection.execute("ROLLBACK TO SAVEPOINT role_operation")
                    self.connection.execute("RELEASE SAVEPOINT role_operation")
                    owned = self.connection.execute(
                        "SELECT EXISTS (SELECT 1 FROM pg_class "
                        "WHERE relowner IN (SELECT oid FROM pg_roles "
                        "WHERE rolname IN ('azd_app', 'privilege_parent', 'privilege_middle')))",
                    ).fetchone()[0]
                    self.assertFalse(owned)
                    with self.assertRaisesRegex(RuntimeError, "must not have admin, DDL, TEMP"):
                        bootstrap.validate_runtime_privileges(self.connection)

    def test_temporary_schema_lookalikes_do_not_hide_create_privileges(self):
        from psycopg import sql

        for depth in (0, 1, 2):
            with self.subTest(depth=depth), self.connection.transaction(force_rollback=True):
                target = self.initialize_roles(depth)
                connection = self.connection
                connection.execute("CREATE SCHEMA pgxtempyapp")
                connection.execute(sql.SQL("GRANT CREATE ON SCHEMA pgxtempyapp TO {}").format(
                    sql.Identifier(target)))
                self.assertTrue(connection.execute(
                    "SELECT has_schema_privilege(%s, 'pgxtempyapp', 'CREATE')", (target,),
                ).fetchone()[0])
                with self.assertRaisesRegex(RuntimeError, "must not have admin, DDL, TEMP"):
                    bootstrap.validate_runtime_privileges(connection)

    def test_administrative_memberships(self):
        from psycopg import sql

        for depth in (0, 1, 2):
            for flag in ("SUPERUSER", "CREATEDB", "CREATEROLE"):
                with self.subTest(depth=depth, flag=flag), self.connection.transaction(force_rollback=True):
                    target = self.initialize_roles(depth)
                    self.connection.execute(sql.SQL("ALTER ROLE {} {}").format(
                        sql.Identifier(target), sql.SQL(flag)))
                    with self.assertRaisesRegex(RuntimeError, "administrative privileges"):
                        bootstrap.validate_runtime_privileges(self.connection)

    def test_non_relation_ownership(self):
        from psycopg import sql

        objects = {
            "FUNCTION": ("owned()", "CREATE FUNCTION privilege_test.owned() RETURNS integer "
                         "LANGUAGE sql AS 'SELECT 1'"),
            "PROCEDURE": ("owned()", "CREATE PROCEDURE privilege_test.owned() "
                          "LANGUAGE plpgsql AS 'BEGIN NULL; END'"),
            "DOMAIN": ("owned", "CREATE DOMAIN privilege_test.owned AS integer"),
            "TYPE": ("owned", "CREATE TYPE privilege_test.owned AS ENUM ('value')"),
        }
        for depth in (0, 1, 2):
            for kind, (name, create) in objects.items():
                with self.subTest(depth=depth, kind=kind), self.connection.transaction(force_rollback=True):
                    target = self.initialize_roles(depth)
                    self.connection.execute(create)
                    self.connection.execute(sql.SQL("GRANT USAGE, CREATE ON SCHEMA privilege_test TO {}").format(
                        sql.Identifier(target)))
                    self.connection.execute(sql.SQL("ALTER {} privilege_test.{} OWNER TO {}").format(
                        sql.SQL(kind), sql.SQL(name), sql.Identifier(target)))
                    self.connection.execute(sql.SQL("REVOKE CREATE ON SCHEMA privilege_test FROM {}").format(
                        sql.Identifier(target)))
                    self.assertEqual(self.connection.execute(
                        "SELECT has_schema_privilege(%s, 'privilege_test', 'CREATE'), "
                        "EXISTS (SELECT 1 FROM pg_class WHERE relowner = (SELECT oid FROM pg_roles WHERE rolname = %s))",
                        (target, target),
                    ).fetchone(), (False, False))
                    # Ownership alone still permits DDL after switching to the reachable role.
                    self.connection.execute("SAVEPOINT owner_operation")
                    self.connection.execute("SET SESSION AUTHORIZATION azd_app")
                    self.connection.execute(sql.SQL("SET ROLE {}").format(sql.Identifier(target)))
                    self.connection.execute(sql.SQL("DROP {} privilege_test.{}").format(sql.SQL(kind), sql.SQL(name)))
                    self.connection.execute("ROLLBACK TO SAVEPOINT owner_operation")
                    self.connection.execute("RELEASE SAVEPOINT owner_operation")
                    with self.assertRaisesRegex(RuntimeError, "object ownership"):
                        bootstrap.validate_runtime_privileges(self.connection)

    def test_recreated_role_recovers_grants_after_failed_attempt(self):
        import psycopg

        principal = "00000000-0000-0000-0000-000000000002"
        env = {
            "AZURE_SUBSCRIPTION_ID": "example-subscription",
            "AZURE_RESOURCE_GROUP": "rg-example",
            "SPRING_DATABASE_SERVER_NAME": "pg-example",
            "SPRING_APPLICATION_PRINCIPAL_ID": principal,
            "SPRING_APPLICATION_ID": "/example/app",
            "SPRING_DATABASE_HOST": "example.postgres.database.azure.com",
            "SPRING_DATABASE_ADMIN_NAME": "admin@example.invalid",
        }
        database = self.connection.execute("SELECT current_database()").fetchone()[0]
        settings = {"engine": "postgresql", "auth": "managedIdentity", "database": database}
        script = (
            "INSERT INTO privilege_test.bootstrap_runs DEFAULT VALUES; "
            "GRANT USAGE ON SCHEMA privilege_test TO azd_app; "
            "GRANT SELECT ON privilege_test.items TO azd_app; "
            "DO $recovery$ BEGIN "
            "IF (SELECT fail FROM privilege_test.recovery_control) THEN "
            "GRANT CREATE ON SCHEMA privilege_test TO azd_app; "
            "END IF; END $recovery$;"
        )
        creations = []

        def mapping_query(statement, parameters):
            cursor = MagicMock()
            if "pgaadauth_list_principals" in statement:
                role = self.connection.execute("SELECT oid FROM pg_roles WHERE rolname = 'azd_app'").fetchone()
                cursor.fetchone.return_value = (principal, 0) if role else None
            elif "pgaadauth_create_principal_with_oid" in statement:
                self.assertEqual(parameters, ("azd_app", principal))
                # The Azure function is unavailable locally. Execute its role creation through
                # a real independent connection, committing before the application transaction.
                self.connection.execute("CREATE ROLE azd_app NOLOGIN NOINHERIT")
                creations.append(self.connection.execute(
                    "SELECT oid FROM pg_roles WHERE rolname = 'azd_app'",
                ).fetchone()[0])
            else:
                self.fail("Unexpected identity mapping query")
            return cursor

        def provision():
            application = psycopg.connect(self.dsn, connect_timeout=10)
            mapping = MagicMock()
            mapping.__enter__.return_value = mapping
            mapping.execute.side_effect = mapping_query
            responses = [
                json.dumps({"identity": {"type": "SystemAssigned", "principalId": principal},
                            "properties": {"outboundIpAddresses": ["203.0.113.10"]}}),
                json.dumps({"accessToken": "unused-test-token", "tenant": "test-tenant"}),
                json.dumps({"tenantId": "test-tenant"}),
                "",
            ]
            with patch("psycopg.connect", side_effect=[mapping, application]), \
                    patch.object(bootstrap, "command", side_effect=responses), \
                    patch.object(bootstrap, "bootstrap_sql", return_value=script), \
                    patch.object(bootstrap, "set_value", side_effect=env.__setitem__), patch("builtins.print"):
                bootstrap.bootstrap(settings, env)

        with self.connection.transaction():
            self.initialize_roles(0)
            self.connection.execute("CREATE TABLE privilege_test.items (value integer)")
            self.connection.execute("INSERT INTO privilege_test.items VALUES (42)")
            self.connection.execute("CREATE TABLE privilege_test.bootstrap_runs (id integer DEFAULT 1)")
            self.connection.execute("CREATE TABLE privilege_test.recovery_control (fail boolean)")
            self.connection.execute("INSERT INTO privilege_test.recovery_control VALUES (false)")
        try:
            provision()
            original_digest = env["SPRING_DATABASE_BOOTSTRAP_DIGEST"]
            original_oid = self.connection.execute(
                "SELECT oid FROM pg_roles WHERE rolname = 'azd_app'",
            ).fetchone()[0]
            with self.connection.transaction():
                self.connection.execute("DROP OWNED BY azd_app")
                self.connection.execute("DROP ROLE azd_app")
                self.connection.execute("UPDATE privilege_test.recovery_control SET fail = true")

            # Same host, database, principal, SQL and stored digest; only the SQL role disappeared.
            # Its replacement commits even though validation rejects the application transaction.
            with self.assertRaisesRegex(RuntimeError, "must not have admin, DDL, TEMP"):
                provision()
            self.assertEqual(len(creations), 1)
            self.assertNotEqual(creations[0], original_oid)
            self.assertEqual(env["SPRING_DATABASE_BOOTSTRAP_DIGEST"], original_digest)
            self.assertEqual(self.connection.execute(
                "SELECT has_schema_privilege('azd_app', 'privilege_test', 'USAGE'), "
                "has_table_privilege('azd_app', 'privilege_test.items', 'SELECT')",
            ).fetchone(), (False, False))
            self.assertEqual(self.connection.execute(
                "SELECT count(*) FROM privilege_test.bootstrap_runs",
            ).fetchone(), (1,))

            self.connection.execute("UPDATE privilege_test.recovery_control SET fail = false")
            provision()
            self.assertEqual(len(creations), 1)  # Retry sees the already-committed replacement mapping.
            self.assertNotEqual(env["SPRING_DATABASE_BOOTSTRAP_DIGEST"], original_digest)
            with self.connection.transaction(force_rollback=True):
                self.connection.execute("SET SESSION AUTHORIZATION azd_app")
                self.assertEqual(self.connection.execute("SELECT value FROM privilege_test.items").fetchone(), (42,))
            self.assertEqual(self.connection.execute(
                "SELECT count(*) FROM privilege_test.bootstrap_runs",
            ).fetchone(), (2,))
            provision()
            self.assertEqual(self.connection.execute(
                "SELECT count(*) FROM privilege_test.bootstrap_runs",
            ).fetchone(), (2,))
        finally:
            with self.connection.transaction():
                self.connection.execute("DROP SCHEMA privilege_test CASCADE")
                self.connection.execute("DROP OWNED BY azd_app, privilege_parent, azure_pg_admin")
                self.connection.execute("DROP ROLE azd_app, privilege_parent, azure_pg_admin")

    def test_bootstrap_transaction_boundaries_and_rollback(self):
        import psycopg
        from psycopg import sql

        principal = "00000000-0000-0000-0000-000000000002"
        env = {
            "AZURE_SUBSCRIPTION_ID": "example-subscription",
            "AZURE_RESOURCE_GROUP": "rg-example",
            "SPRING_DATABASE_SERVER_NAME": "pg-example",
            "SPRING_APPLICATION_PRINCIPAL_ID": principal,
            "SPRING_APPLICATION_ID": "/example/app",
            "SPRING_DATABASE_HOST": "example.postgres.database.azure.com",
            "SPRING_DATABASE_ADMIN_NAME": "admin@example.invalid",
        }
        database = self.connection.execute("SELECT current_database()").fetchone()[0]
        settings = {"engine": "postgresql", "auth": "managedIdentity", "database": database}
        unsafe = ("CREATE TABLE privilege_test.bootstrap_marker (value text); "
                  "GRANT CREATE ON SCHEMA privilege_test TO azd_app; ")
        scripts = [
            ("unsafe grants", unsafe, "must not have admin, DDL, TEMP"),
            ("wrapped", "BEGIN; " + unsafe + "COMMIT;", "cannot control transactions"),
            ("start transaction", "START TRANSACTION; " + unsafe + "END;", "cannot control transactions"),
        ]
        # Each command follows real writes, so rejecting it must also undo earlier statements.
        for control in (
            "COMMIT", "COMMIT AND CHAIN", "END", "ROLLBACK", "ABORT", "SAVEPOINT user_savepoint",
            "RELEASE SAVEPOINT user_savepoint", "ROLLBACK TO SAVEPOINT user_savepoint",
            "PREPARE TRANSACTION 'bootstrap_case'", "COMMIT PREPARED 'bootstrap_case'",
            "ROLLBACK PREPARED 'bootstrap_case'",
        ):
            scripts.append((control, unsafe + control + ";", "cannot control transactions"))
        scripts.extend([
            ("nested block", unsafe + "DO $nested$ BEGIN COMMIT; END $nested$;", "cannot control transactions"),
            ("nested procedure", unsafe + "CREATE PROCEDURE privilege_test.commit_changes() LANGUAGE plpgsql "
             "AS $procedure$ BEGIN COMMIT; END $procedure$; CALL privilege_test.commit_changes();",
             "cannot control transactions"),
        ])
        # Commit only the fixture setup, allowing an independent connection to observe whether the
        # production bootstrap accidentally commits rejected DDL/grants. Everything is disposable.
        with self.connection.transaction():
            self.initialize_roles(0)
            self.connection.execute(sql.SQL("GRANT TEMP ON DATABASE {} TO PUBLIC").format(sql.Identifier(database)))
        baseline = self.connection.execute(
            "SELECT datacl FROM pg_database WHERE datname = current_database()",
        ).fetchone()
        try:
            for name, script, message in scripts:
                with self.subTest(name=name):
                    application = psycopg.connect(self.dsn, connect_timeout=10)
                    mapping = MagicMock()
                    mapping.__enter__.return_value = mapping
                    mapping.execute.return_value.fetchone.return_value = (principal, 0)
                    responses = [
                        json.dumps({"identity": {"type": "SystemAssigned", "principalId": principal},
                                    "properties": {"outboundIpAddresses": ["203.0.113.10"]}}),
                        json.dumps({"accessToken": "unused-test-token", "tenant": "test-tenant"}),
                        json.dumps({"tenantId": "test-tenant"}),
                        "",
                    ]
                    # Only Azure identity/token acquisition and its mapping connection are mocked.
                    # The application connection, transaction, supplied SQL and validator are real.
                    with patch("psycopg.connect", side_effect=[mapping, application]), \
                            patch.object(bootstrap, "command", side_effect=responses), \
                            patch.object(bootstrap, "bootstrap_sql", return_value=script), \
                            patch.object(bootstrap, "set_value") as setter, patch("builtins.print") as printer:
                        with self.assertRaisesRegex(RuntimeError, message):
                            bootstrap.bootstrap(settings, env)
                        setter.assert_not_called()
                        printer.assert_not_called()
                    self.assertTrue(application.closed)
                    self.assertEqual(self.connection.execute(
                        "SELECT to_regclass('privilege_test.bootstrap_marker'), "
                        "has_schema_privilege('azd_app', 'privilege_test', 'CREATE')",
                    ).fetchone(), (None, False))
                    self.assertEqual(self.connection.execute(
                        "SELECT datacl FROM pg_database WHERE datname = current_database()",
                    ).fetchone(), baseline)

            # Valid BEGIN/END blocks, comments, strings and dollar-quoted routine bodies are SQL,
            # not transaction commands. They must still execute without keyword-based rejection.
            safe = (
                "CREATE TABLE privilege_test.bootstrap_marker (value text); "
                "-- COMMIT is harmless in a comment\n"
                "DO $body$ BEGIN INSERT INTO privilege_test.bootstrap_marker "
                "VALUES ('BEGIN; COMMIT; $azd_bootstrap$ ''quoted'''); END $body$; "
                "CREATE FUNCTION privilege_test.safe() RETURNS text LANGUAGE sql "
                "AS $function$ SELECT 'ROLLBACK'::text $function$; "
                "GRANT USAGE ON SCHEMA privilege_test TO azd_app; "
                "GRANT SELECT ON privilege_test.bootstrap_marker TO azd_app;"
            )
            with psycopg.connect(self.dsn, connect_timeout=10) as application:
                application.execute(sql.SQL("REVOKE ALL ON DATABASE {} FROM PUBLIC").format(sql.Identifier(database)))
                bootstrap.execute_bootstrap_sql(application, safe)
                bootstrap.validate_runtime_privileges(application)
            self.assertEqual(self.connection.execute(
                "SELECT value FROM privilege_test.bootstrap_marker",
            ).fetchone(), ("BEGIN; COMMIT; $azd_bootstrap$ 'quoted'",))
        finally:
            with self.connection.transaction():
                self.connection.execute("DROP SCHEMA privilege_test CASCADE")
                self.connection.execute("DROP OWNED BY azd_app, privilege_parent, azure_pg_admin")
                self.connection.execute("DROP ROLE azd_app, privilege_parent, azure_pg_admin")


if __name__ == "__main__":
    unittest.main()
