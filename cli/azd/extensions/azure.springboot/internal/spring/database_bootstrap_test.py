# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import traceback
import unittest
from unittest.mock import MagicMock, patch


spec = importlib.util.spec_from_file_location(
    "bootstrap",
    Path(__file__).parent / "template" / ".azd-spring" / "bootstrap-postgresql-managed-identity.py",
)
bootstrap = importlib.util.module_from_spec(spec)
with patch.object(sys, "dont_write_bytecode", True):
    spec.loader.exec_module(bootstrap)


def prerequisite_environment(root):
    script = Path(root) / "bootstrap.sql"
    script.write_text("GRANT SELECT ON items TO azd_app;", encoding="utf-8")
    return {
        "AZURE_SUBSCRIPTION_ID": "example-subscription",
        "SPRING_DATABASE_ADMIN_OID": "00000000-0000-0000-0000-000000000001",
        "SPRING_DATABASE_ADMIN_NAME": "admin@example.invalid",
        "SPRING_DATABASE_BOOTSTRAP_SQL": str(script),
    }


class BootstrapTests(unittest.TestCase):
    def test_bootstrap_firewall_cleanup_on_success_and_failure(self):
        env = {
            "AZURE_SUBSCRIPTION_ID": "example-subscription",
            "AZURE_RESOURCE_GROUP": "rg-example",
            "SPRING_DATABASE_SERVER_NAME": "pg-example",
        }
        settings = {"engine": "postgresql", "auth": "managedIdentity"}
        for sql_fails in (False, True):
            for cleanup_fails in (False, True):
                with self.subTest(sql_fails=sql_fails, cleanup_fails=cleanup_fails), \
                        patch.object(bootstrap, "bootstrap_database", return_value="verified-digest",
                                     side_effect=RuntimeError("SQL bootstrap rejected") if sql_fails else None), \
                        patch.object(bootstrap, "command",
                                     side_effect=RuntimeError("az postgres failed") if cleanup_fails else None) as command, \
                        patch.object(bootstrap, "set_value") as setter, patch("builtins.print") as printer:
                    if sql_fails or cleanup_fails:
                        message = "Failed to remove temporary PostgreSQL firewall" if cleanup_fails else "SQL bootstrap rejected"
                        with self.assertRaisesRegex(RuntimeError, message) as error:
                            bootstrap.bootstrap(settings, env)
                        if sql_fails and cleanup_fails:
                            formatted = "".join(traceback.format_exception(error.exception))
                            self.assertIn("SQL bootstrap rejected", formatted)
                            self.assertIn("Failed to remove temporary PostgreSQL firewall", formatted)
                        setter.assert_not_called()
                        printer.assert_not_called()
                    else:
                        bootstrap.bootstrap(settings, env)
                        setter.assert_called_once_with("SPRING_DATABASE_BOOTSTRAP_DIGEST", "verified-digest")
                        printer.assert_called_once()
                    command.assert_called_once_with(
                        "az", "postgres", "flexible-server", "firewall-rule", "delete",
                        "--subscription", env["AZURE_SUBSCRIPTION_ID"],
                        "--resource-group", env["AZURE_RESOURCE_GROUP"],
                        "--server-name", env["SPRING_DATABASE_SERVER_NAME"],
                        "--name", "bootstrap-local-ip", "--yes",
                    )

    def test_missing_cleanup_identity_does_not_start_bootstrap(self):
        env = {
            "AZURE_SUBSCRIPTION_ID": "example-subscription",
            "AZURE_RESOURCE_GROUP": "rg-example",
            "SPRING_DATABASE_SERVER_NAME": "pg-example",
        }
        for key in env:
            with self.subTest(key=key), \
                    patch.object(bootstrap, "bootstrap_database") as database, \
                    patch.object(bootstrap, "command") as command:
                with self.assertRaisesRegex(RuntimeError, key):
                    bootstrap.bootstrap({"engine": "postgresql", "auth": "managedIdentity"},
                                        {name: value for name, value in env.items() if name != key})
                database.assert_not_called()
                command.assert_not_called()

    def test_other_database_modes_are_rejected(self):
        for engine, auth in (("none", "password"), ("mysql", "password"), ("postgresql", "password")):
            with self.subTest(engine=engine, auth=auth), patch.object(bootstrap, "set_value") as setter:
                with self.assertRaisesRegex(RuntimeError, "Unsupported"):
                    bootstrap.prepare({"engine": engine, "auth": auth}, {})
                with self.assertRaisesRegex(RuntimeError, "requires PostgreSQL managed identity"):
                    bootstrap.bootstrap({"engine": engine, "auth": auth}, {})
                setter.assert_not_called()

    def test_identity_prerequisites_and_conflicts(self):
        with tempfile.TemporaryDirectory() as root:
            env = prerequisite_environment(root)
            settings = {"engine": "postgresql", "auth": "managedIdentity"}
            with patch.object(bootstrap.importlib.util, "find_spec", return_value=object()), \
                    patch.object(bootstrap, "command", return_value="unused-token") as command, \
                    patch("builtins.print"):
                bootstrap.prepare(settings, env)
                with self.assertRaisesRegex(RuntimeError, "Remove SPRING_DATABASE_PASSWORD"):
                    bootstrap.prepare(settings, env | {"SPRING_DATABASE_PASSWORD": "unused"})
                Path(env["SPRING_DATABASE_BOOTSTRAP_SQL"]).write_text("", encoding="utf-8")
                with self.assertRaisesRegex(RuntimeError, "reviewed schema"):
                    bootstrap.prepare(settings, env)
                command.assert_called_once_with(
                    "az", "account", "get-access-token", "--resource-type", "oss-rdbms",
                    "--subscription", env["AZURE_SUBSCRIPTION_ID"], "--output", "none")

    def test_cli_prerequisites_before_provisioning(self):
        token, diagnostic = "sensitive-preflight-token", "sensitive-auth-diagnostic"
        settings = {"engine": "postgresql", "auth": "managedIdentity"}
        timeout = bootstrap.subprocess.TimeoutExpired("az account", 120, output=token, stderr=diagnostic)
        for name, executable, code, failure in (
            ("authenticated", "az", 0, None),
            ("missing CLI", None, 0, None),
            ("unusable login", "az", 1, None),
            ("token request timeout", "az", 0, timeout),
        ):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as root:
                env = prerequisite_environment(root)
                result = MagicMock(returncode=code, stdout=token, stderr=diagnostic)
                with patch.object(bootstrap.importlib.util, "find_spec", return_value=object()), \
                        patch.object(bootstrap.shutil, "which", return_value=executable), \
                        patch.object(bootstrap.subprocess, "run", return_value=result, side_effect=failure) as runner, \
                        patch.object(bootstrap, "set_value") as setter, patch("builtins.print") as printer:
                    if executable is None or code != 0 or failure is not None:
                        with self.assertRaisesRegex(RuntimeError, "Azure CLI.*az login") as error:
                            bootstrap.prepare(settings, env)
                        formatted = "".join(traceback.format_exception(error.exception))
                        self.assertNotIn(token, formatted)
                        self.assertNotIn(diagnostic, formatted)
                        printer.assert_not_called()
                    else:
                        bootstrap.prepare(settings, env)
                        printer.assert_called_once_with(
                            "Managed-identity prerequisites checked; schema/grant SQL executes as the Entra administrator.")
                    setter.assert_not_called()
                    if executable is None:
                        runner.assert_not_called()
                    else:
                        runner.assert_called_once()
                        self.assertEqual(runner.call_args.args[0], [
                            "az", "account", "get-access-token", "--resource-type", "oss-rdbms",
                            "--subscription", env["AZURE_SUBSCRIPTION_ID"], "--output", "none",
                        ])
                        self.assertTrue(runner.call_args.kwargs["capture_output"])

    def test_preflight_requires_selected_subscription(self):
        with tempfile.TemporaryDirectory() as root:
            env = prerequisite_environment(root)
            env.pop("AZURE_SUBSCRIPTION_ID")
            with patch.object(bootstrap.importlib.util, "find_spec", return_value=object()), \
                    patch.object(bootstrap, "command") as command, patch("builtins.print") as printer:
                with self.assertRaisesRegex(RuntimeError, "AZURE_SUBSCRIPTION_ID"):
                    bootstrap.prepare({"engine": "postgresql", "auth": "managedIdentity"}, env)
                command.assert_not_called()
                printer.assert_not_called()

    def test_command_errors_do_not_disclose_captured_credentials(self):
        result = MagicMock(returncode=1, stdout="secret-token", stderr="secret-password")
        with patch.object(bootstrap.shutil, "which", return_value="azd"), \
                patch.object(bootstrap.subprocess, "run", return_value=result):
            with self.assertRaises(RuntimeError) as error:
                bootstrap.command("azd", "env", "set", "PASSWORD", "secret-input")
            message = str(error.exception)
            self.assertNotIn("secret", message)
            self.assertIn("azd env failed", message)

    def test_command_timeout_does_not_disclose_credentials(self):
        credential = "sensitive-timeout-value"
        arguments = ["azd", "env", "set", "SPRING_DATABASE_PASSWORD", credential]
        timeout = bootstrap.subprocess.TimeoutExpired(arguments, 120, output=credential, stderr=credential)
        with patch.object(bootstrap.shutil, "which", return_value="azd"), \
                patch.object(bootstrap.subprocess, "run", side_effect=timeout), \
                patch("builtins.print") as printer:
            try:
                bootstrap.set_value("SPRING_DATABASE_BOOTSTRAP_DIGEST", credential)
            except RuntimeError as error:
                message = "".join(traceback.format_exception(error))
                self.assertNotIn(credential, message)
                self.assertIn("set_value", message)
                self.assertIn("azd env timed out", message)
                self.assertTrue(error.__suppress_context__)
                self.assertIsNone(error.__cause__)
            else:
                self.fail("Command execution must fail when storing an environment value times out")
            printer.assert_not_called()

    def test_mysql_identity_is_rejected(self):
        with self.assertRaisesRegex(RuntimeError, "Unsupported"):
            bootstrap.prepare({"engine": "mysql", "auth": "managedIdentity"}, {})

    def test_identity_bootstrap_mapping_grants_repeat_and_unsafe_privileges(self):
        principal = "00000000-0000-0000-0000-000000000002"
        host = "example.postgres.database.azure.com"
        script = "GRANT USAGE ON SCHEMA public TO azd_app;"
        env = {
            "AZURE_SUBSCRIPTION_ID": "example-subscription",
            "AZURE_RESOURCE_GROUP": "rg-example",
            "SPRING_DATABASE_SERVER_NAME": "pg-example",
            "SPRING_APPLICATION_PRINCIPAL_ID": principal,
            "SPRING_APPLICATION_ID": "/example/app",
            "SPRING_DATABASE_HOST": host,
            "SPRING_DATABASE_ADMIN_NAME": "admin@example.invalid",
        }
        settings = {"engine": "postgresql", "auth": "managedIdentity", "database": "catalog"}

        def command(tool, *arguments):
            if arguments[:4] == ("postgres", "flexible-server", "firewall-rule", "delete"):
                return ""
            if arguments[:2] == ("containerapp", "show"):
                return json.dumps({"identity": {"type": "SystemAssigned", "principalId": principal},
                                   "properties": {"outboundIpAddresses": ["203.0.113.10"]}})
            if arguments[:2] == ("account", "get-access-token"):
                return json.dumps({"accessToken": "runtime-token", "tenant": "test-tenant"})
            if arguments[:2] == ("account", "show"):
                return json.dumps({"tenantId": "test-tenant"})
            raise AssertionError("Unexpected CLI call")

        safe = (False, False, False, False, False)
        safe_flags = (False, False, False)
        cases = (
            ("initial", None, "16384", False, safe_flags, safe),
            ("repeat", "16384", "16384", True, safe_flags, safe),
            ("recreated database", "16384", "16402", True, safe_flags, safe),
            ("direct unsafe privilege", None, "16384", False, safe_flags,
             (True, False, False, False, False)),
            ("repeat with schema creation membership", "16384", "16384", True, safe_flags,
             (False, False, False, True, False)),
            ("runtime administrator", None, "16384", False, (True, False, False), safe),
            ("repeat with administrative membership", "16384", "16384", True, (False, True, False), safe),
            ("missing runtime role", None, "16384", False, (None, None, None), safe),
        )
        for name, stored_database_oid, database_oid, mapping_exists, flags, unsafe in cases:
            postgres, database = MagicMock(), MagicMock()
            postgres.__enter__.return_value = postgres
            database.__enter__.return_value = database
            postgres.execute.return_value.fetchone.return_value = (principal, 0) if mapping_exists else None
            database.execute.return_value.fetchone.side_effect = [
                (database_oid,), (24576,), flags, unsafe,
            ]
            psycopg = MagicMock()
            psycopg.connect.side_effect = [postgres, database]
            current = env.copy()
            if stored_database_oid is not None:
                current["SPRING_DATABASE_BOOTSTRAP_DIGEST"] = bootstrap.bootstrap_digest(
                    host, settings["database"], stored_database_oid, 24576, principal, script,
                )
            with self.subTest(name=name), \
                    patch.dict(sys.modules, {"psycopg": psycopg, "certifi": MagicMock()}), \
                    patch.object(bootstrap, "command", side_effect=command), \
                    patch.object(bootstrap, "bootstrap_sql", return_value=script), \
                    patch.object(bootstrap, "execute_bootstrap_sql") as execute_script, \
                    patch.object(bootstrap, "set_value") as setter, patch("builtins.print"):
                if flags != safe_flags or any(unsafe):
                    with self.assertRaisesRegex(RuntimeError, "must not have admin"):
                        bootstrap.bootstrap(settings, current)
                    setter.assert_not_called()
                    self.assertIs(database.__exit__.call_args.args[0], RuntimeError)
                else:
                    bootstrap.bootstrap(settings, current)
            self.assertEqual(psycopg.connect.call_args.kwargs["password"], "runtime-token")
            self.assertEqual(psycopg.connect.call_args.kwargs["sslmode"], "verify-full")
            if stored_database_oid != database_oid:
                execute_script.assert_called_once_with(database, script)
            else:
                execute_script.assert_not_called()
            if not mapping_exists:
                self.assertIn("pgaadauth_create_principal_with_oid", postgres.execute.call_args.args[0])
                self.assertEqual(postgres.execute.call_args.args[1], ("azd_app", principal))
            if flags == safe_flags and not any(unsafe):
                self.assertEqual(setter.call_args.args[0], "SPRING_DATABASE_BOOTSTRAP_DIGEST")
                self.assertEqual(
                    setter.call_args.args[1],
                    bootstrap.bootstrap_digest(host, settings["database"], database_oid, 24576, principal, script),
                )

    def test_mapping_conflict_does_not_execute_schema(self):
        principal = "00000000-0000-0000-0000-000000000002"
        env = {
            "AZURE_SUBSCRIPTION_ID": "example-subscription",
            "AZURE_RESOURCE_GROUP": "rg-example",
            "SPRING_DATABASE_SERVER_NAME": "pg-example",
            "SPRING_APPLICATION_PRINCIPAL_ID": principal, "SPRING_APPLICATION_ID": "/example/app",
            "SPRING_DATABASE_HOST": "example.postgres.database.azure.com",
            "SPRING_DATABASE_ADMIN_NAME": "admin@example.invalid",
        }
        postgres = MagicMock()
        postgres.__enter__.return_value = postgres
        postgres.execute.return_value.fetchone.return_value = ("another-object-id", 0)
        psycopg = MagicMock()
        psycopg.connect.return_value = postgres
        responses = [
            json.dumps({"identity": {"type": "SystemAssigned", "principalId": principal},
                        "properties": {"outboundIpAddresses": ["203.0.113.10"]}}),
            json.dumps({"accessToken": "runtime-token", "tenant": "test-tenant"}),
            json.dumps({"tenantId": "test-tenant"}),
            "",
        ]
        with patch.dict(sys.modules, {"psycopg": psycopg, "certifi": MagicMock()}), \
                patch.object(bootstrap, "command", side_effect=responses), \
                patch.object(bootstrap, "bootstrap_sql", return_value="-- reviewed SQL"), \
                patch.object(bootstrap, "set_value") as setter:
            with self.assertRaisesRegex(RuntimeError, "different identity"):
                bootstrap.bootstrap({"engine": "postgresql", "auth": "managedIdentity", "database": "catalog"}, env)
            self.assertEqual(psycopg.connect.call_count, 1)
            setter.assert_not_called()


if __name__ == "__main__":
    unittest.main()
