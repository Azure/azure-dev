# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

from contextlib import redirect_stderr
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

import scenario
import service


owned_process = scenario.proof_module.process_module


class OwnedProcessTests(unittest.TestCase):
    def environment(self):
        return {key: value for key, value in os.environ.items()
                if key.upper() in ("PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "LANG")}

    def command(self, root, *, parent_exits, inherited_streams=False, exit_code=0):
        heartbeat = root / "heartbeat"
        child = root / "child.py"
        child.write_text(
            "import time\nfrom pathlib import Path\n"
            f"path = Path({str(heartbeat)!r})\n"
            "while True:\n"
            "    with path.open('a') as stream: stream.write('alive\\n')\n"
            "    time.sleep(0.02)\n", encoding="utf-8")
        parent = root / "parent.py"
        streams = "" if inherited_streams else ", stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL"
        parent.write_text(
            "import subprocess,sys,time\nfrom pathlib import Path\n"
            f"subprocess.Popen([sys.executable, {str(child)!r}]{streams})\n"
            f"while not Path({str(heartbeat)!r}).exists(): time.sleep(0.01)\n"
            "print('parent ready', flush=True)\n"
            + (f"Path({str(root / 'parent-exit')!r}).write_text(str(time.monotonic()))\n"
               f"raise SystemExit({exit_code})\n"
               if parent_exits else "time.sleep(30)\n"), encoding="utf-8")
        return [sys.executable, str(parent)], heartbeat

    def test_timeout_terminates_descendant_activity_before_returning(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            args, heartbeat = self.command(root, parent_exits=False)
            start = time.monotonic()
            with self.assertRaises(subprocess.TimeoutExpired) as raised:
                owned_process.run(args, cwd=root, env=self.environment(), timeout=2)
            self.assertIn(b"parent ready", raised.exception.output)
            self.assertTrue(heartbeat.is_file(), "The actual descendant must start before the timeout")
            before = heartbeat.read_bytes()
            time.sleep(0.15)
            self.assertEqual(heartbeat.read_bytes(), before, "No descendant activity may survive the owned timeout")
            self.assertLess(time.monotonic() - start, 8)

    def test_normal_parent_exit_also_terminates_orphaned_descendants(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            args, heartbeat = self.command(root, parent_exits=True)
            result = owned_process.run(args, cwd=root, env=self.environment(), timeout=5, text=True)
            self.assertEqual(result.returncode, 0)
            self.assertIn("parent ready", result.stdout)
            before = heartbeat.read_bytes()
            time.sleep(0.15)
            self.assertEqual(heartbeat.read_bytes(), before)

    def test_normal_exit_does_not_wait_for_inherited_descendant_pipe_eof(self):
        for exit_code in (0, 7):
            with self.subTest(exit_code=exit_code), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                args, heartbeat = self.command(root, parent_exits=True, inherited_streams=True, exit_code=exit_code)
                result = owned_process.run(args, cwd=root, env=self.environment(), timeout=6, text=True)
                self.assertLess(time.monotonic() - float((root / "parent-exit").read_text()), 2)
                self.assertEqual(result.returncode, exit_code)
                self.assertIn("parent ready", result.stdout)
                before = heartbeat.read_bytes()
                time.sleep(0.15)
                self.assertEqual(heartbeat.read_bytes(), before)

    def test_missing_executable_has_fixed_diagnostics_without_traceback_or_path(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            result = owned_process.run([str(root / "private-missing-executable")],
                                       cwd=root, env=self.environment(), timeout=5, text=True)
            self.assertEqual(result.returncode, 127)
            self.assertEqual(result.stdout, "")
            self.assertEqual(result.stderr.strip(), "Owned CLI executable could not be started.")

    def test_permission_denied_respects_platform_launch_exit_contract(self):
        for platform, expected in (("posix", 126), ("nt", 127)):
            with self.subTest(platform=platform):
                output = io.StringIO()
                with mock.patch.object(owned_process.sys, "stdin", io.StringIO(json.dumps({"argv": ["private-path"]}))), \
                     mock.patch.object(owned_process.subprocess, "run", side_effect=PermissionError("private-detail")), \
                     mock.patch.object(owned_process.os, "name", platform), redirect_stderr(output):
                    self.assertEqual(owned_process.main(), expected)
                self.assertEqual(output.getvalue().strip(), "Owned CLI executable could not be started.")

    @unittest.skipUnless(os.name == "nt", "Inject a secondary error after real Windows job drain")
    def test_timeout_and_secondary_cleanup_failure_survive_both_callers(self):
        drain = owned_process.WindowsJob.wait_empty

        def failed_drain(job, timeout):
            drain(job, timeout)
            raise OSError("private-cleanup-diagnostic")

        for caller in ("helper", "offline", "service"):
            with self.subTest(caller=caller), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                args, heartbeat = self.command(root, parent_exits=False)
                with mock.patch.object(owned_process.WindowsJob, "wait_empty", failed_drain):
                    if caller == "helper":
                        with self.assertRaises(subprocess.TimeoutExpired) as raised:
                            owned_process.run(args, cwd=root, env=self.environment(), timeout=2)
                        self.assertIn(b"parent ready", raised.exception.output)
                        errors = raised.exception.__notes__
                    elif caller == "offline":
                        runner = scenario.proof_module.Proof(root, root, {})
                        runner.azd = Path(sys.executable)
                        with self.assertRaises(subprocess.TimeoutExpired):
                            runner.run("timeout fixture", args[1:], timeout=2)
                        self.assertTrue(runner.commands[-1]["timedOut"])
                        errors = runner.commands[-1]["processCleanupErrors"]
                    else:
                        report = {}
                        runner = service.Driver(Path(sys.executable), root / "auth", root, 2, 10, report)
                        with self.assertRaisesRegex(RuntimeError, "timed out"):
                            runner("timeout fixture", args[1:], output_format=None)
                        self.assertTrue(report["commands"][-1]["timedOut"])
                        errors = report["commands"][-1]["processCleanupErrors"]
                self.assertIn("cleanup also failed (OSError)", errors[0])
                self.assertNotIn("private-cleanup-diagnostic", str(errors))
                before = heartbeat.read_bytes()
                time.sleep(0.1)
                self.assertEqual(heartbeat.read_bytes(), before)

    @unittest.skipUnless(os.name == "nt", "Inject a secondary error after real Windows job drain")
    def test_success_cleanup_failure_is_not_hidden_by_an_ambient_handled_exception(self):
        drain = owned_process.WindowsJob.wait_empty

        def failed_drain(job, timeout):
            drain(job, timeout)
            raise OSError("cleanup failed")

        with tempfile.TemporaryDirectory() as root:
            try:
                raise ValueError("already handled")
            except ValueError:
                with mock.patch.object(owned_process.WindowsJob, "wait_empty", failed_drain):
                    with self.assertRaisesRegex(OSError, "cleanup failed"):
                        owned_process.run([sys.executable, "-c", "print('done')"],
                                          cwd=Path(root), env=self.environment(), timeout=5)

    def test_both_cli_callers_return_only_after_inherited_child_activity_stops(self):
        for caller in ("offline", "service"):
            with self.subTest(caller=caller), tempfile.TemporaryDirectory() as root:
                root = Path(root)
                args, heartbeat = self.command(root, parent_exits=True, inherited_streams=True)
                if caller == "offline":
                    runner = scenario.proof_module.Proof(root, root, {})
                    runner.azd = Path(sys.executable)
                    runner.run("owned child fixture", args[1:], timeout=6)
                    record = runner.commands[-1]
                else:
                    report = {}
                    runner = service.Driver(Path(sys.executable), root / "auth", root, 6, 10, report)
                    runner("owned child fixture", args[1:], output_format=None)
                    record = report["commands"][-1]
                self.assertEqual(record["exitCode"], 0)
                self.assertLess(time.monotonic() - float((root / "parent-exit").read_text()), 2)
                before = heartbeat.read_bytes()
                time.sleep(0.15)
                self.assertEqual(heartbeat.read_bytes(), before)

    @unittest.skipUnless(os.name == "nt", "Windows job assignment boundary")
    def test_failed_job_assignment_never_releases_the_cli_launch_request(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            marker = root / "must-not-launch"
            args = [sys.executable, "-c", f"from pathlib import Path; Path({str(marker)!r}).write_text('launched')"]
            with mock.patch.object(owned_process.WindowsJob, "assign", side_effect=OSError("Assignment refused")):
                with self.assertRaisesRegex(OSError, "Assignment refused"):
                    owned_process.run(args, cwd=root, env=self.environment(), timeout=5)
            self.assertFalse(marker.exists())


if __name__ == "__main__":
    unittest.main()
