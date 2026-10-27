"""Opt-in bounded invocation helper; never logs tokens or deletes resources."""

import argparse
import json
from pathlib import Path
import re
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--subscription", required=True)
    parser.add_argument("--project-endpoint", required=True)
    parser.add_argument("--agent", required=True)
    parser.add_argument("--session-id")
    parser.add_argument("--prefix", default=f"act-task-{uuid.uuid4().hex[:8]}")
    parser.add_argument("--delay-seconds", type=int, default=90)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    endpoint = args.project_endpoint.rstrip("/")
    url = urllib.parse.urlsplit(endpoint)
    if url.scheme != "https" or not url.hostname or not url.hostname.endswith(".services.ai.azure.com"):
        parser.error("expected an HTTPS Foundry project endpoint")
    if url.username or url.password or url.query or url.fragment:
        parser.error("project endpoint must not contain credentials, query, or fragment")
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,80}", args.prefix):
        parser.error("prefix must be 1-80 letters, numbers, underscores, or hyphens")
    if not 1 <= args.delay_seconds <= 180:
        parser.error("delay must be 1-180 seconds")
    args.output.mkdir(parents=True, exist_ok=False)
    token = subprocess.check_output(
        ["az", "account", "get-access-token", "--subscription", args.subscription,
         "--resource", "https://ai.azure.com", "--query", "accessToken", "-o", "tsv"],
        text=True,
    ).strip()

    def request(method, path, body=None):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(
            endpoint + path, data=data, method=method,
            headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json",
                     "Foundry-Features": "Routines=V2Preview"},
        )
        try:
            with urllib.request.urlopen(req, timeout=60) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            # Never include request headers or the access token in diagnostics.
            raise RuntimeError(f"{method} failed with HTTP {error.code}") from None

    def save(name, value):
        (args.output / name).write_text(json.dumps(value, indent=2) + "\n")

    agent = urllib.parse.quote(args.agent, safe="")
    query = {"api-version": "v1"}
    if args.session_id:
        query["agent_session_id"] = args.session_id
    invocation_path = f"/agents/{agent}/endpoint/protocols/invocations?" + urllib.parse.urlencode(query)
    identities = [
        {"task_id": f"{args.prefix}-{i:02d}", "input_id": f"{args.prefix}-{i:02d}-turn1",
         "delay_seconds": args.delay_seconds}
        for i in range(1, 4)
    ]
    save("identities.json", identities)
    for i, body in enumerate(identities, 1):
        save(f"invoke-{i}.json", request("POST", invocation_path, body))
    ids = {x["task_id"] for x in identities}

    def snapshot():
        # Dedicated fixture IDs allow exact reads without relying on list ordering.
        return [request("GET", "/tasks/" + urllib.parse.quote(task_id, safe="") + "?api-version=v1")
                for task_id in sorted(ids)]

    records = snapshot()
    save("tasks-initial.json", records)
    print("Initial:", [(x["id"], x["status"]) for x in records])
    deadline = time.monotonic() + args.delay_seconds + 45
    while time.monotonic() < deadline:
        if all(x["status"] == "suspended" for x in records):
            save("tasks-suspended.json", records)
            print("Suspended:", sorted(ids))
            return
        time.sleep(3)
        records = snapshot()
    save("tasks-timeout.json", records)
    raise SystemExit("Timed out waiting for suspended records; all records/resources retained")


if __name__ == "__main__":
    main()
