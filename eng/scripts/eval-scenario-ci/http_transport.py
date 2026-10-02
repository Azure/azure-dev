#!/usr/bin/env python3
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Private HTTP transport; the parent enforces and terminates its absolute deadline."""

import base64
import json
import sys
import urllib.error
import urllib.parse
import urllib.request


MAX_RESPONSE_BYTES = 1024 * 1024


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def perform(payload):
    url = urllib.parse.urlsplit(payload["url"])
    if (url.scheme != "https" or not url.hostname or not url.hostname.endswith(".services.ai.azure.com")
            or url.port is not None or url.username or url.password or url.fragment):
        raise ValueError("Unsupported service endpoint")
    if payload["method"] not in ("GET", "POST", "DELETE"):
        raise ValueError("Unsupported service operation")
    request = urllib.request.Request(
        payload["url"], method=payload["method"],
        data=json.dumps(payload["body"]).encode("utf-8") if payload["body"] is not None else None,
        headers={"Authorization": "Bearer " + payload["token"], "Content-Type": "application/json"},
    )
    try:
        with urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect()).open(
                request, timeout=payload["timeout"]) as response:
            status = response.status
            raw = response.read(MAX_RESPONSE_BYTES + 1) if payload["readJson"] else b""
            if len(raw) > MAX_RESPONSE_BYTES:
                raise ValueError("Service metadata response exceeded the bounded transport limit")
    except urllib.error.HTTPError as error:
        status, raw = error.code, b""
        error.close()
    return {"status": status, "body": base64.b64encode(raw).decode("ascii")}


def main():
    try:
        result = perform(json.load(sys.stdin))
    except (OSError, ValueError, KeyError, TypeError, urllib.error.URLError):
        # Never send request URLs, headers, tokens or raw service errors to logs.
        print('{"transportError":"Service transport failed; remote outcome is unknown"}')
        return 1
    json.dump(result, sys.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
