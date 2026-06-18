#!/usr/bin/env python3
"""Inject a pre-obtained access token into az CLI for CI use.

Usage (in CI):
  Set env vars AZ_ACCESS_TOKEN, AZ_TENANT_ID, AZ_SUB_ID, then run this script.
  After running, `az account get-access-token` will return the injected token.

The token is valid for ~1 hour. Obtain fresh token locally with:
  az account get-access-token --resource https://management.azure.com/ --query accessToken -o tsv
"""

import json
import os
import sys
import base64
import time
import stat
from pathlib import Path


def decode_jwt_payload(token: str) -> dict:
    """Decode JWT payload without verification (just to extract claims)."""
    parts = token.split(".")
    if len(parts) != 3:
        sys.exit("ERROR: AZ_ACCESS_TOKEN is not a valid JWT (expected 3 parts)")
    payload = parts[1]
    # Add padding
    payload += "=" * (4 - len(payload) % 4)
    return json.loads(base64.urlsafe_b64decode(payload))


def main():
    token = os.environ.get("AZ_ACCESS_TOKEN", "").strip()
    ai_token = os.environ.get("AZ_AI_TOKEN", "").strip()
    tenant_id = os.environ.get("AZ_TENANT_ID", "").strip()
    sub_id = os.environ.get("AZ_SUB_ID", "").strip()

    if not token:
        sys.exit("ERROR: AZ_ACCESS_TOKEN env var is empty")
    if not tenant_id:
        sys.exit("ERROR: AZ_TENANT_ID env var is empty")
    if not sub_id:
        sys.exit("ERROR: AZ_SUB_ID env var is empty")

    # Extract oid and upn from token
    claims = decode_jwt_payload(token)
    oid = claims.get("oid", "unknown")
    upn = claims.get("upn", claims.get("unique_name", "user@unknown"))
    exp = claims.get("exp", int(time.time()) + 3600)

    print(f"Token subject: {upn}")
    print(f"Token expires: {time.strftime('%Y-%m-%d %H:%M:%S', time.gmtime(exp))} UTC")
    remaining = exp - int(time.time())
    print(f"Time remaining: {remaining // 60}m {remaining % 60}s")
    if remaining < 600:
        print("WARNING: Token expires in less than 10 minutes!")

    home = Path.home()
    azure_dir = home / ".azure"
    azure_dir.mkdir(parents=True, exist_ok=True)

    # 1. Write azureProfile.json (for az account show)
    profile = {
        "installationId": "e2e-ci-test",
        "subscriptions": [
            {
                "id": sub_id,
                "name": "E2E Test Subscription",
                "state": "Enabled",
                "tenantId": tenant_id,
                "user": {"name": upn, "type": "user"},
                "isDefault": True,
                "environmentName": "AzureCloud",
                "homeTenantId": tenant_id,
                "managedByTenants": [],
            }
        ],
    }
    profile_path = azure_dir / "azureProfile.json"
    profile_path.write_text(json.dumps(profile, indent=2))
    print(f"Wrote {profile_path}")

    # 2. Write the token to a file for the wrapper
    token_path = azure_dir / "injected_token"
    token_path.write_text(token)
    token_path.chmod(0o600)

    # 3. Write the expiry timestamp
    expiry_str = time.strftime("%Y-%m-%d %H:%M:%S.000000", time.gmtime(exp))
    (azure_dir / "injected_expiry").write_text(expiry_str)

    # 3b. Write AI token if provided (for https://ai.azure.com/ audience)
    if ai_token:
        ai_claims = decode_jwt_payload(ai_token)
        ai_exp = ai_claims.get("exp", exp)
        (azure_dir / "injected_ai_token").write_text(ai_token)
        ai_expiry_str = time.strftime("%Y-%m-%d %H:%M:%S.000000", time.gmtime(ai_exp))
        (azure_dir / "injected_ai_expiry").write_text(ai_expiry_str)
        print(f"AI token expires: {time.strftime('%Y-%m-%d %H:%M:%S', time.gmtime(ai_exp))} UTC")

    # 4. Write az wrapper script that intercepts get-access-token calls.
    #    The MSAL cache approach is fragile (key format must match exactly),
    #    so we use a wrapper that returns our token for any resource request.
    wrapper_dir = home / "az-wrapper"
    wrapper_dir.mkdir(parents=True, exist_ok=True)

    wrapper_script = f'''#!/bin/bash
# az CLI wrapper that returns injected access token for get-access-token calls.
# Supports multiple resource audiences (management + AI).
# Falls through to real az for everything else.
REAL_AZ=$(which -a az 2>/dev/null | grep -v az-wrapper | head -1)
REAL_AZ="${{REAL_AZ:-/usr/bin/az}}"

if echo "$*" | grep -q "account get-access-token"; then
    # Check if AI resource is requested
    if echo "$*" | grep -q "ai.azure.com"; then
        AI_TOKEN_FILE="{azure_dir}/injected_ai_token"
        AI_EXPIRY_FILE="{azure_dir}/injected_ai_expiry"
        if [ -f "$AI_TOKEN_FILE" ]; then
            TOKEN=$(cat "$AI_TOKEN_FILE")
            EXPIRY=$(cat "$AI_EXPIRY_FILE")
        else
            # Fall back to management token
            TOKEN=$(cat {azure_dir}/injected_token)
            EXPIRY=$(cat {azure_dir}/injected_expiry)
        fi
    else
        TOKEN=$(cat {azure_dir}/injected_token)
        EXPIRY=$(cat {azure_dir}/injected_expiry)
    fi
    cat <<EOJSON
{{
  "accessToken": "$TOKEN",
  "expiresOn": "$EXPIRY",
  "expires_on": {exp},
  "subscription": "{sub_id}",
  "tenant": "{tenant_id}",
  "tokenType": "Bearer"
}}
EOJSON
else
    exec "$REAL_AZ" "$@"
fi
'''
    wrapper_path = wrapper_dir / "az"
    wrapper_path.write_text(wrapper_script)
    wrapper_path.chmod(0o755)

    # 5. Prepend wrapper dir to GITHUB_PATH so it's in PATH for subsequent steps
    github_path = os.environ.get("GITHUB_PATH", "")
    if github_path:
        with open(github_path, "a") as f:
            f.write(f"{wrapper_dir}\n")
        print(f"Added {wrapper_dir} to GITHUB_PATH")
    else:
        print(f"WARNING: GITHUB_PATH not set. Manually add to PATH: export PATH={wrapper_dir}:$PATH")

    print(f"Wrote az wrapper to {wrapper_path}")
    print("az CLI token injection complete.")


if __name__ == "__main__":
    main()
