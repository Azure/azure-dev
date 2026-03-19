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


def get_access_token():
    """Get Azure access token using Azure CLI or managed identity."""
    # Try az cli first
    try:
        import subprocess
        result = subprocess.run(
            ["az", "account", "get-access-token", "--query", "accessToken", "-o", "tsv"],
            capture_output=True, text=True, check=True
        )
        return result.stdout.strip()
    except Exception:
        pass

    # Fall back to AZURE_ACCESS_TOKEN env var
    token = os.environ.get("AZURE_ACCESS_TOKEN")
    if token:
        return token

    raise RuntimeError("No Azure credentials available. Run 'az login' or set AZURE_ACCESS_TOKEN.")


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
    except HTTPError:
        return []


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
    if not check_resource_group_exists(subscription_id, resource_group, token):
        return {"score": 0.0, "reason": f"Resource group '{resource_group}' does not exist"}

    if not expected_resources:
        return {"score": 1.0, "reason": f"Resource group '{resource_group}' exists"}

    # Check expected resources
    actual_types = list_resources(subscription_id, resource_group, token)
    found = sum(1 for expected in expected_resources if expected in actual_types)
    score = found / len(expected_resources)

    missing = [r for r in expected_resources if r not in actual_types]
    if missing:
        return {"score": score, "reason": f"Missing resources: {', '.join(missing)}"}

    return {"score": 1.0, "reason": "All expected resources found"}
