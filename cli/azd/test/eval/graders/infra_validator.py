"""
Waza code grader: Validates Azure infrastructure exists after azd provision.

Usage in task YAML:
  graders:
    - type: code
      config:
        language: python
        file: graders/infra_validator.py
        params:
          resource_group: "rg-myapp-dev"
          expected_resources:
            - "Microsoft.Web/sites"
            - "Microsoft.DocumentDB/databaseAccounts"
"""
import os
import json
from urllib.request import Request, urlopen
from urllib.error import HTTPError

from azure_auth import get_access_token


def check_resource_group_exists(subscription_id: str, resource_group: str, token: str) -> bool:
    """Check if a resource group exists."""
    url = (
        f"https://management.azure.com/subscriptions/{subscription_id}"
        f"/resourcegroups/{resource_group}?api-version=2021-04-01"
    )
    req = Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        urlopen(req)
        return True
    except HTTPError as e:
        if e.code == 404:
            return False
        raise


def list_resources(subscription_id: str, resource_group: str, token: str) -> list:
    """List all resources in a resource group."""
    url = (
        f"https://management.azure.com/subscriptions/{subscription_id}"
        f"/resourceGroups/{resource_group}/resources?api-version=2021-04-01"
    )
    req = Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        resp = urlopen(req)
        data = json.loads(resp.read())
        return [r["type"] for r in data.get("value", [])]
    except HTTPError as e:
        if e.code == 404:
            return []  # Resource group doesn't exist
        raise  # Auth failures, permission errors should surface


def grade(context: dict) -> dict:
    """Waza grader entry point."""
    params = context.get("params", {})
    subscription_id = params.get("subscription_id", os.environ.get("AZURE_SUBSCRIPTION_ID", ""))
    resource_group = params.get("resource_group", "")
    expected_resources = params.get("expected_resources", [])

    if not subscription_id or not resource_group:
        return {"score": 0.0, "reason": "Missing subscription_id or resource_group parameter"}

    try:
        token = get_access_token()
    except RuntimeError as e:
        return {"score": 0.0, "reason": str(e)}

    # Check resource group exists
    try:
        rg_exists = check_resource_group_exists(subscription_id, resource_group, token)
    except HTTPError as e:
        return {"score": 0.0, "reason": f"HTTP error checking resource group: {e.code} {e.reason}"}

    if not rg_exists:
        return {"score": 0.0, "reason": f"Resource group '{resource_group}' does not exist"}

    if not expected_resources:
        return {"score": 1.0, "reason": f"Resource group '{resource_group}' exists"}

    # Check expected resources
    try:
        actual_types = list_resources(subscription_id, resource_group, token)
    except HTTPError as e:
        return {"score": 0.0, "reason": f"HTTP error listing resources: {e.code} {e.reason}"}
    found = sum(1 for expected in expected_resources if expected in actual_types)
    score = found / len(expected_resources)

    missing = [r for r in expected_resources if r not in actual_types]
    if missing:
        return {"score": score, "reason": f"Missing resources: {', '.join(missing)}"}

    return {"score": 1.0, "reason": "All expected resources found"}
