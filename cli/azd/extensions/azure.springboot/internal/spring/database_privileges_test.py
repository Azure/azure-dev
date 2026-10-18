# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Privilege regressions against a disposable PostgreSQL 15 server, without Azure."""

import os
import unittest

from database_bootstrap_test import bootstrap


class DatabasePrivilegeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        import psycopg

        dsn = os.environ.get("SPRING_TEST_POSTGRES_DSN")
        if not dsn:
            raise RuntimeError("Set SPRING_TEST_POSTGRES_DSN to a disposable PostgreSQL 15 superuser connection")
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


if __name__ == "__main__":
    unittest.main()
