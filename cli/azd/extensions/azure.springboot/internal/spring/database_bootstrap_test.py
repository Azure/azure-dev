# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import traceback
import unittest
from unittest.mock import MagicMock, patch


spec = importlib.util.spec_from_file_location(
    "bootstrap", Path(__file__).parent / "template" / ".azd-spring" / "database.py")
bootstrap = importlib.util.module_from_spec(spec)
with patch.object(sys, "dont_write_bytecode", True):
    spec.loader.exec_module(bootstrap)


class BootstrapTests(unittest.TestCase):
    def test_embedded_does_not_prepare_credentials(self):
        with patch.object(bootstrap, "set_value") as setter:
            bootstrap.prepare({"engine": "none"}, {})
            setter.assert_not_called()

    def test_password_is_created_once(self):
        settings = {"engine": "mysql", "auth": "password"}
        with patch.object(bootstrap, "set_value") as setter, patch("builtins.print"):
            bootstrap.prepare(settings, {})
            self.assertEqual(setter.call_args.args[0], "SPRING_DATABASE_PASSWORD")
            self.assertGreaterEqual(len(setter.call_args.args[1]), 32)
            setter.reset_mock()
            bootstrap.prepare(settings, {"SPRING_DATABASE_PASSWORD": "existing"})
            setter.assert_not_called()

    def test_identity_prerequisites_and_conflicts(self):
        with tempfile.TemporaryDirectory() as root:
            script = Path(root) / "bootstrap.sql"
            script.write_text("GRANT SELECT ON items TO azd_app;", encoding="utf-8")
            env = {
                "SPRING_DATABASE_ADMIN_OID": "00000000-0000-0000-0000-000000000001",
                "SPRING_DATABASE_ADMIN_NAME": "admin@example.invalid",
                "SPRING_DATABASE_BOOTSTRAP_IP": "203.0.113.10",
                "SPRING_DATABASE_BOOTSTRAP_SQL": str(script),
            }
            settings = {"engine": "postgresql", "auth": "managedIdentity"}
            with patch.object(bootstrap.importlib.util, "find_spec", return_value=object()), patch("builtins.print"):
                bootstrap.prepare(settings, env)
                with self.assertRaisesRegex(RuntimeError, "Remove SPRING_DATABASE_PASSWORD"):
                    bootstrap.prepare(settings, env | {"SPRING_DATABASE_PASSWORD": "unused"})
                with self.assertRaisesRegex(RuntimeError, "must not be 0.0.0.0"):
                    bootstrap.prepare(settings, env | {"SPRING_DATABASE_BOOTSTRAP_IP": "0.0.0.0"})
                script.write_text("", encoding="utf-8")
                with self.assertRaisesRegex(RuntimeError, "reviewed schema"):
                    bootstrap.prepare(settings, env)

    def test_command_errors_do_not_disclose_captured_credentials(self):
        result = MagicMock(returncode=1, stdout="secret-token", stderr="secret-password")
        with patch.object(bootstrap.shutil, "which", return_value="azd"), \
                patch.object(bootstrap.subprocess, "run", return_value=result):
            with self.assertRaises(RuntimeError) as error:
                bootstrap.command("azd", "env", "set", "PASSWORD", "secret-input")
            message = str(error.exception)
            self.assertNotIn("secret", message)
            self.assertIn("azd env failed", message)

    def test_password_preparation_timeout_does_not_disclose_credentials(self):
        credential = "sensitive-timeout-value"
        arguments = ["azd", "env", "set", "SPRING_DATABASE_PASSWORD", credential]
        timeout = bootstrap.subprocess.TimeoutExpired(arguments, 120, output=credential, stderr=credential)
        with patch.object(bootstrap.shutil, "which", return_value="azd"), \
                patch.object(bootstrap.subprocess, "run", side_effect=timeout), \
                patch.object(bootstrap.secrets, "token_urlsafe", return_value=credential), \
                patch("builtins.print") as printer:
            try:
                bootstrap.prepare({"engine": "mysql", "auth": "password"}, {})
            except RuntimeError as error:
                message = "".join(traceback.format_exception(error))
                self.assertNotIn(credential, message)
                self.assertIn("set_value", message)
                self.assertIn("azd env timed out", message)
                self.assertTrue(error.__suppress_context__)
                self.assertIsNone(error.__cause__)
            else:
                self.fail("Password preparation must fail when storing the generated credential times out")
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
            "SPRING_APPLICATION_PRINCIPAL_ID": principal,
            "SPRING_APPLICATION_ID": "/example/app",
            "SPRING_DATABASE_HOST": host,
            "SPRING_DATABASE_ADMIN_NAME": "admin@example.invalid",
        }
        settings = {"engine": "postgresql", "auth": "managedIdentity", "database": "catalog"}

        def command(tool, *arguments):
            if arguments[:2] == ("containerapp", "show"):
                return json.dumps({"identity": {"type": "SystemAssigned", "principalId": principal},
                                   "properties": {"outboundIpAddresses": ["203.0.113.10"]}})
            if arguments[:2] == ("account", "get-access-token"):
                return json.dumps({"accessToken": "runtime-token", "tenant": "test-tenant"})
            if arguments[:2] == ("account", "show"):
                return json.dumps({"tenantId": "test-tenant"})
            raise AssertionError("Unexpected CLI call")

        for repeat, unsafe in ((False, False), (True, False), (False, True)):
            postgres, database = MagicMock(), MagicMock()
            postgres.__enter__.return_value = postgres
            database.__enter__.return_value = database
            postgres.execute.return_value.fetchone.return_value = (principal, 0) if repeat else None
            database.execute.return_value.fetchone.side_effect = [
                (False, False, False), (unsafe, False, False, False, False),
            ]
            psycopg = MagicMock()
            psycopg.connect.side_effect = [postgres, database]
            current = env.copy()
            if repeat:
                current["SPRING_DATABASE_BOOTSTRAP_DIGEST"] = hashlib.sha256(
                    (host + "\n" + principal + "\n" + script).encode()).hexdigest()
            with patch.dict(sys.modules, {"psycopg": psycopg, "certifi": MagicMock()}), \
                    patch.object(bootstrap, "command", side_effect=command), \
                    patch.object(bootstrap, "bootstrap_sql", return_value=script), \
                    patch.object(bootstrap, "set_value") as setter, patch("builtins.print"):
                if unsafe:
                    with self.assertRaisesRegex(RuntimeError, "must not have admin"):
                        bootstrap.bootstrap(settings, current)
                    setter.assert_not_called()
                    self.assertIs(database.__exit__.call_args.args[0], RuntimeError)
                else:
                    bootstrap.bootstrap(settings, current)
            self.assertEqual(psycopg.connect.call_args.kwargs["password"], "runtime-token")
            self.assertEqual(psycopg.connect.call_args.kwargs["sslmode"], "verify-full")
            executed = [call.args[0] for call in database.execute.call_args_list]
            self.assertEqual(script in executed, not repeat)
            if not repeat:
                self.assertIn("pgaadauth_create_principal_with_oid", postgres.execute.call_args.args[0])
                self.assertEqual(postgres.execute.call_args.args[1], ("azd_app", principal))
            if not unsafe:
                self.assertEqual(setter.call_args.args[0], "SPRING_DATABASE_BOOTSTRAP_DIGEST")

    def test_mapping_conflict_does_not_execute_schema(self):
        principal = "00000000-0000-0000-0000-000000000002"
        env = {
            "AZURE_SUBSCRIPTION_ID": "example-subscription",
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
