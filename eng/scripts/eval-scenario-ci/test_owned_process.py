# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

import scenario


owned_process = scenario.proof_module.process_module


class OwnedProcessTests(unittest.TestCase):
    def environment(self):
        return {key: value for key, value in os.environ.items()
                if key.upper() in ("PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "LANG")}

    def command(self, root, *, parent_exits):
        heartbeat = root / "heartbeat"
        child = root / "child.py"
        child.write_text(
            "import time\nfrom pathlib import Path\n"
            f"path = Path({str(heartbeat)!r})\n"
            "while True:\n"
            "    with path.open('a') as stream: stream.write('alive\\n')\n"
            "    time.sleep(0.02)\n", encoding="utf-8")
        parent = root / "parent.py"
        parent.write_text(
            "import subprocess,sys,time\nfrom pathlib import Path\n"
            f"subprocess.Popen([sys.executable, {str(child)!r}], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)\n"
            f"while not Path({str(heartbeat)!r}).exists(): time.sleep(0.01)\n"
            "print('parent ready', flush=True)\n"
            + ("" if parent_exits else "time.sleep(30)\n"), encoding="utf-8")
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
