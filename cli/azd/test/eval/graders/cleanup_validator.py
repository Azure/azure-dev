"""
Waza code grader: Validates Azure resources are cleaned up after azd down.

Confirms the resource group no longer exists by expecting a 404 from the
Azure ARM API. Returns 1.0 if fully cleaned up, 0.0 if resources remain.

Usage in task YAML:
  graders:
    - type: code
      config:
        language: python
        file: graders/cleanup_validator.py
        params:
          resource_group: "rg-myapp-dev"
"""
import os
import json
from urllib.request import Request, urlopen
from urllib.error import HTTPError

from azure_auth import get_access_token


def check_resource_group_exists(subscription_id: str, resource_group: str, token: str) -> dict:
    """Check resource group status. Returns dict with exists flag and details."""
    url = (
        f"https://management.azure.com/subscriptions/{subscription_id}"
        f"/resourcegroups/{resource_group}?api-version=2021-04-01"
    )
    req = Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        resp = urlopen(req)
        data = json.loads(resp.read())
        state = data.get("properties", {}).get("provisioningState", "Unknown")
        return {"exists": True, "state": state}
    except HTTPError as e:
        if e.code == 404:
            return {"exists": False, "state": "Deleted"}
        raise


def list_remaining_resources(subscription_id: str, resource_group: str, token: str) -> list:
    """List any resources still present in the resource group."""
    url = (
        f"https://management.azure.com/subscriptions/{subscription_id}"
        f"/resourceGroups/{resource_group}/resources?api-version=2021-04-01"
    )
    req = Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        resp = urlopen(req)
        data = json.loads(resp.read())
        return [
            {"name": r.get("name", ""), "type": r.get("type", "")}
            for r in data.get("value", [])
        ]
    except HTTPError:
        return []


def grade(context: dict) -> dict:
    """Waza grader entry point."""
    params = context.get("params", {})
    subscription_id = params.get("subscription_id", os.environ.get("AZURE_SUBSCRIPTION_ID", ""))
    resource_group = params.get("resource_group", "")

    if not subscription_id or not resource_group:
        return {"score": 0.0, "reason": "Missing subscription_id or resource_group parameter"}

    try:
        token = get_access_token()
    except RuntimeError as e:
        return {"score": 0.0, "reason": str(e)}

    try:
        rg_status = check_resource_group_exists(subscription_id, resource_group, token)
    except HTTPError as e:
        return {"score": 0.0, "reason": f"HTTP error checking resource group: {e.code} {e.reason}"}

    if not rg_status["exists"]:
        return {"score": 1.0, "reason": f"Resource group '{resource_group}' successfully deleted"}

    # Resource group still exists — check if it's being deleted
    if rg_status["state"] == "Deleting":
        return {
            "score": 0.5,
            "reason": f"Resource group '{resource_group}' is still being deleted (state: Deleting)",
        }

    # Resource group exists with resources remaining
    remaining = list_remaining_resources(subscription_id, resource_group, token)
    if remaining:
        details = ", ".join(f"{r['type']}/{r['name']}" for r in remaining[:10])
        suffix = f" (and {len(remaining) - 10} more)" if len(remaining) > 10 else ""
        return {
            "score": 0.0,
            "reason": f"Resource group '{resource_group}' still exists with resources: {details}{suffix}",
        }

    return {
        "score": 0.0,
        "reason": f"Resource group '{resource_group}' still exists (state: {rg_status['state']})",
    }
