# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

import base64
import io
import json
from pathlib import Path
import subprocess
import tempfile
import time
import unittest
from unittest import mock

import http_transport
import service


class HttpTransportTests(unittest.TestCase):
    def payload(self):
        return {
            "url": "https://fixture.services.ai.azure.com/api/projects/fixture/agents/ci-owned/versions/9?api-version=v1",
            "method": "DELETE", "token": "mock-private-token", "body": None, "timeout": 5, "readJson": True,
        }

    def test_transport_keeps_exact_route_and_refuses_redirects(self):
        opener = mock.Mock()
        opener.open.side_effect = http_transport.urllib.error.HTTPError(
            self.payload()["url"], 404, "gone", {}, io.BytesIO())
        with mock.patch.object(http_transport.urllib.request, "build_opener", return_value=opener) as build:
            result = http_transport.perform(self.payload())
        self.assertEqual(result, {"status": 404, "body": ""})
        opener.open.assert_called_once()
        request = opener.open.call_args.args[0]
        self.assertEqual(request.method, "DELETE")
        self.assertEqual(request.full_url, self.payload()["url"])
        self.assertEqual(build.call_args.args[0].proxies, {})
        redirect = build.call_args.args[1]
        self.assertIsNone(redirect.redirect_request(request, None, 302, "", {}, "https://elsewhere.invalid"))

    def test_transport_rejects_oversized_metadata(self):
        response = mock.MagicMock()
        response.__enter__.return_value.status = 200
        response.__enter__.return_value.read.return_value = b"x" * (http_transport.MAX_RESPONSE_BYTES + 1)
        opener = mock.Mock()
        opener.open.return_value = response
        with mock.patch.object(http_transport.urllib.request, "build_opener", return_value=opener):
            with self.assertRaisesRegex(ValueError, "bounded transport limit"):
                http_transport.perform(self.payload())

    def test_nonempty_null_is_not_empty_delete_success(self):
        for raw in (b"null", b"[]", b"false"):
            with self.subTest(raw=raw), tempfile.TemporaryDirectory() as root:
                workspace = Path(root)
                report = {}
                driver = service.Driver(Path("azd"), workspace / "auth", workspace, 5, 30, report)
                token = subprocess.CompletedProcess([], 0, b'{"token":"mock-private-token"}', b"")
                response = subprocess.CompletedProcess([], 0, json.dumps({
                    "status": 200, "body": base64.b64encode(raw).decode(),
                }).encode(), b"")
                with mock.patch.object(service.subprocess, "run", return_value=token), \
                     mock.patch.object(service, "transport_exchange", return_value=response):
                    with self.assertRaisesRegex(RuntimeError, "non-object service metadata"):
                        driver.request("delete only owned prompt-agent version", "DELETE",
                                       "https://fixture.services.ai.azure.com/api/projects/fixture",
                                       "/agents/ci-owned/versions/9?api-version=v1", "tenant",
                                       accepted=(200, 204, 404))
                self.assertNotIn("mock-private-token", json.dumps(report))

    def test_slow_pipes_headers_body_and_exit_share_one_absolute_deadline(self):
        real_popen = subprocess.Popen
        for phase in ("stdin", "headers", "body", "exit"):
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as root:
                workspace = Path(root)
                entered = workspace / "entered"
                helper = workspace / "slow_transport.py"
                helper.write_text(
                    "import sys,time\nfrom pathlib import Path\n"
                    f"sys.path.insert(0, {str(Path(http_transport.__file__).parent)!r})\n"
                    "import http_transport\n"
                    "def stall():\n"
                    f"    Path({str(entered)!r}).write_text('entered')\n"
                    "    time.sleep(30)\n"
                    "class Response:\n"
                    "    status=200\n"
                    "    def __enter__(self): return self\n"
                    "    def __exit__(self,*args): return False\n"
                    "    def read(self,limit):\n"
                    f"        if {phase!r} == 'body': stall()\n"
                    "        return b'{}'\n"
                    "class Opener:\n"
                    "    def open(self,*args,**kwargs):\n"
                    f"        if {phase!r} == 'headers': stall()\n"
                    "        return Response()\n"
                    "http_transport.urllib.request.build_opener=lambda *args: Opener()\n"
                    f"if {phase!r} == 'stdin': stall()\n"
                    "result=http_transport.main()\n"
                    f"if {phase!r} == 'exit':\n"
                    "    sys.stdout.flush()\n"
                    "    stall()\n"
                    "raise SystemExit(result)\n",
                    encoding="utf-8",
                )
                report, processes = {}, []
                driver = service.Driver(Path("azd"), workspace / "auth", workspace, 3, 3, report)
                token = subprocess.CompletedProcess([], 0, b'{"token":"mock-private-token"}', b"")

                def spawn(*args, **kwargs):
                    process = real_popen(*args, **kwargs)
                    processes.append(process)
                    return process

                start = time.monotonic()
                with mock.patch.object(service, "HTTP_TRANSPORT", helper), \
                     mock.patch.object(service.subprocess, "run", return_value=token), \
                     mock.patch.object(subprocess, "Popen", side_effect=spawn):
                    with self.assertRaisesRegex(RuntimeError, "absolute HTTP deadline"):
                        driver.request("create owned prompt-agent version", "POST",
                                       "https://fixture.services.ai.azure.com/api/projects/fixture",
                                       "/agents/ci-owned/versions?api-version=v1", "tenant",
                                       body={"definition": {"instructions": "x" * (512 * 1024)}},
                                       accepted=(200, 201))
                self.assertTrue(entered.is_file(), "The mocked slow HTTP phase must actually have started")
                self.assertLess(time.monotonic() - start, 8)
                self.assertEqual(len(processes), 1)
                self.assertIsNotNone(processes[0].poll(), "The owned transport process must be terminated and reaped")
                self.assertTrue(report["commands"][-1]["timedOut"])
                self.assertNotIn("mock-private-token", json.dumps(report))

    def test_crashed_transport_is_reaped_and_private_diagnostics_are_not_published(self):
        real_popen = subprocess.Popen
        with tempfile.TemporaryDirectory() as root:
            workspace = Path(root)
            helper = workspace / "crashed_transport.py"
            helper.write_text("import sys\nsys.stdin.read()\nsys.stderr.write('mock-private-token')\n"
                              "raise SystemExit(42)\n", encoding="utf-8")
            report, processes = {}, []
            driver = service.Driver(Path("azd"), workspace / "auth", workspace, 5, 10, report)
            token = subprocess.CompletedProcess([], 0, b'{"token":"mock-private-token"}', b"")

            def spawn(*args, **kwargs):
                process = real_popen(*args, **kwargs)
                processes.append(process)
                return process

            with mock.patch.object(service, "HTTP_TRANSPORT", helper), \
                 mock.patch.object(service.subprocess, "run", return_value=token), \
                 mock.patch.object(subprocess, "Popen", side_effect=spawn):
                with self.assertRaisesRegex(RuntimeError, "transport failed") as raised:
                    driver.request("create owned prompt-agent version", "POST",
                                   "https://fixture.services.ai.azure.com/api/projects/fixture",
                                   "/agents/ci-owned/versions?api-version=v1", "tenant",
                                   body={"definition": {}}, accepted=(200, 201))
            self.assertEqual(len(processes), 1)
            self.assertEqual(processes[0].poll(), 42)
            self.assertNotIn("mock-private-token", str(raised.exception) + json.dumps(report))


if __name__ == "__main__":
    unittest.main()
