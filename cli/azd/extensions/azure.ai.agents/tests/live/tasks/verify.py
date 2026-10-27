"""Opt-in Tasks CLI assertions against an existing, dedicated hosted fixture.

Never provisions or deletes resources. Evidence contains synthetic task data only.
"""

import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--azd", required=True, type=Path)
    parser.add_argument("--project-directory", required=True, type=Path)
    parser.add_argument("--project-endpoint", required=True)
    parser.add_argument("--agent", required=True)
    parser.add_argument("--subscription", required=True)
    parser.add_argument("--session-id", required=True)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    env = dict(os.environ, AZURE_DEV_USER_AGENT="microsoft_foundry_skill",
               AZURE_DEV_COLLECT_TELEMETRY="no", NO_COLOR="1", AZD_FORCE_TTY="false")
    results = []

    def cli(label, operation, *flags, outside=False, enabled=True, success=True):
        command_env = dict(env, AZD_ALPHA_ENABLE_AGENTS_LONG_RUNNING=str(enabled).lower())
        command = [str(args.azd.resolve()), "ai", "agent", "tasks", operation, *flags, "--no-prompt"]
        # An empty scratch directory exercises explicit targeting with no checkout.
        with tempfile.TemporaryDirectory(prefix="tasks-cli-outside-") as empty:
            process = subprocess.run(command, cwd=empty if outside else args.project_directory,
                                     env=command_env, text=True, capture_output=True, timeout=90)
        (args.output / f"{label}.stdout").write_text(process.stdout)
        (args.output / f"{label}.stderr").write_text(process.stderr)
        assert (process.returncode == 0) == success, f"{label}: unexpected exit {process.returncode}"
        results.append(label)
        return process.stdout + process.stderr if not success else process.stdout

    def page(label, *flags, **kwargs):
        return json.loads(cli(label, "list", *flags, **kwargs))

    endpoint = ["--project-endpoint", args.project_endpoint]
    target = [*endpoint, "--agent-name", args.agent]
    for operation, extra in [("list", target), ("show", ["unknown", *endpoint])]:
        text = cli(f"gate-off-{operation}", operation, *extra, enabled=False, outside=True, success=False)
        assert "agents.long_running" in text and "disabled" in text

    first = page("first-page", "--limit", "2", "--order", "asc")
    assert len(first["data"]) == 2 and first["has_more"] is True
    assert all(x["agent_name"] == args.agent for x in first["data"])
    assert first["last_id"] != first["data"][-1]["id"]
    second = page("second-page", "--limit", "2", "--order", "asc", "--after", first["last_id"])
    assert second["data"] and not ({x["id"] for x in first["data"]} & {x["id"] for x in second["data"]})
    task_id = first["data"][0]["id"]
    task = json.loads(cli("show-project", "show", task_id))
    assert task["id"] == task_id and "etag" in task and "payload" in task
    remote = json.loads(cli("show-outside", "show", task_id, *endpoint, outside=True))
    assert remote["id"] == task_id and remote["agent_name"] == args.agent
    listed = page("list-outside", *target, outside=True)
    assert listed["data"] and all(x["agent_name"] == args.agent for x in listed["data"])
    missing = cli("missing-agent-outside", "list", *endpoint, outside=True, success=False)
    assert "agent-name" in missing

    for name, flags, predicate in [
        ("status", ["--status", "suspended"], lambda x: x["status"] == "suspended"),
        ("session", ["--session-id", args.session_id], lambda x: x["session_id"] == args.session_id),
        ("source", ["--source-type", "agentserver.task"], lambda x: x["source"]["type"] == "agentserver.task"),
        ("has-error-false", ["--has-error=false"], lambda x: x.get("error") is None),
        ("tags", ["--tag", "task_name:act_tasks_inspection_fixture"],
         lambda x: x["tags"]["task_name"] == "act_tasks_inspection_fixture"),
    ]:
        filtered = page(name, *flags)
        assert filtered["data"] and all(predicate(x) for x in filtered["data"])
    for name, flags in [
        ("tags-and", ["--tag", "task_name:act_tasks_inspection_fixture", "--tag", "unique:non-matching"]),
        ("has-error-true", ["--has-error=true"]),
        ("unknown-session", ["--session-id", "absent-synthetic-session"]),
    ]:
        assert page(name, *flags)["data"] == []
    omitted = page("omit-attachments", "--omit-attachment-values")
    assert all(x.get("attachments") is None or all(v is None for v in x["attachments"].values())
               for x in omitted["data"])
    table = cli("table-list", "list", "--output", "table", "--limit", "2")
    assert "STATUS" in table and "last_id" in table and "payload" not in table
    table = cli("table-show", "show", task_id, "--output", "table")
    assert "Has error: false" in table and "Created:" in table and "payload" not in table
    text = cli("missing-task", "show", "synthetic-missing-task", success=False)
    assert "404" in text or "not found" in text.lower()
    text = cli("invalid-limit", "list", "--limit=101", success=False)
    assert "--limit" in text
    text = cli("missing-environment", "list", "--environment", "synthetic-missing-env", success=False)
    assert "environment" in text.lower()

    # Seed three additional records and inspect an active lease through the built CLI.
    # The helper writes exact identities before POST and waits for suspension.
    seed_dir = args.output / "seed"
    seed_log = (args.output / "seed.log").open("w")
    process = subprocess.Popen([
        "python3", str(Path(__file__).with_name("seed.py")), "--subscription", args.subscription,
        "--project-endpoint", args.project_endpoint, "--agent", args.agent,
        "--session-id", args.session_id, "--prefix", f"cli-task-{uuid.uuid4().hex[:8]}",
        "--delay-seconds", "60", "--output", str(seed_dir),
    ], env=env, stdout=seed_log, stderr=subprocess.STDOUT)
    try:
        deadline = time.monotonic() + 55
        active = None
        while time.monotonic() < deadline:
            current = page(f"active-{len(results)}", "--status", "in_progress")
            if current["data"]:
                active = current["data"][0]
                break
            if process.poll() is not None:
                break
            time.sleep(2)
        assert active is not None and active.get("lease", {}).get("owner"), "No active task lease observed"
        detail = json.loads(cli("active-show", "show", active["id"]))
        assert detail["id"] == active["id"] and detail["status"] == "in_progress"
        owner = active["lease"]["owner"]
        leases = page("lease-owner", "--lease-owner", owner, "--lease-expired=false")
        assert active["id"] in {x["id"] for x in leases["data"]}
        table = cli("active-table", "show", active["id"], "--output", "table")
        assert f"Lease owner: {owner}" in table and "Lease expiry: -" not in table
        assert "Has error: false" in table
        assert process.wait(timeout=150) == 0, "Seeding helper failed"
        final = json.loads(cli("suspended-show", "show", active["id"]))
        assert final["id"] == active["id"] and final["status"] == "suspended"
        assert page("expired-leases", "--lease-expired=true")["data"] == []
    finally:
        # Wait for the bounded helper; never delete records or cloud resources.
        if process.poll() is None:
            process.wait(timeout=150)
        seed_log.close()
    (args.output / "summary.json").write_text(json.dumps({
        "passed": results, "count": len(results), "resources_retained": True,
    }, indent=2) + "\n")
    print(f"PASS: {len(results)} live CLI checks; all resources and records retained.")


if __name__ == "__main__":
    main()
