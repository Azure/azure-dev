"""Local-only redirect regression tests; no Azure credentials or resources."""

import http.server
import threading
import unittest
import urllib.error
import urllib.request

from seed import RejectRedirects


class RejectRedirectsTests(unittest.TestCase):
    def test_authenticated_redirects_are_not_followed(self):
        received = []
        redirected_authorizations = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                received.append(self.path)
                if self.path == "/direct":
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b'{}')
                else:
                    self.send_response(302)
                    self.send_header("Location", target_url)
                    self.end_headers()

            def log_message(self, *_args):
                pass

        class TargetHandler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                received.append("redirect-target")
                redirected_authorizations.append(self.headers.get("Authorization"))
                self.send_response(200)
                self.end_headers()

            def log_message(self, *_args):
                pass

        servers = [
            http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler),
            http.server.ThreadingHTTPServer(("127.0.0.1", 0), TargetHandler),
        ]
        threads = []
        try:
            for server in servers:
                thread = threading.Thread(target=server.serve_forever)
                thread.start()
                threads.append(thread)
            source_url = f"http://127.0.0.1:{servers[0].server_port}"
            opener = urllib.request.build_opener(RejectRedirects())
            with opener.open(source_url + "/direct", timeout=5) as response:
                self.assertEqual(response.status, 200)
                self.assertEqual(response.read(), b'{}')
            for target_url in [
                source_url + "/direct",
                f"http://localhost:{servers[1].server_port}/outside-foundry",
            ]:
                with self.subTest(target=target_url):
                    request = urllib.request.Request(
                        source_url + "/redirect",
                        headers={"Authorization": "Bearer synthetic-test-token"},
                    )
                    with self.assertRaises(urllib.error.HTTPError) as raised:
                        opener.open(request, timeout=5)
                    self.assertEqual(raised.exception.code, 302)
                    raised.exception.close()
            self.assertEqual(redirected_authorizations, [])
            self.assertEqual(received, ["/direct", "/redirect", "/redirect"])
        finally:
            for server in servers:
                server.shutdown()
                server.server_close()
            for thread in threads:
                thread.join(timeout=5)


if __name__ == "__main__":
    unittest.main()
