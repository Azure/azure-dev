"""Shared Azure authentication helper for eval graders."""
import os
import subprocess


def get_access_token() -> str:
    """Get Azure access token using Azure CLI or environment variable.

    Tries `az account get-access-token` first, then falls back to
    the AZURE_ACCESS_TOKEN environment variable.
    """
    try:
        result = subprocess.run(
            ["az", "account", "get-access-token", "--query", "accessToken", "-o", "tsv"],
            capture_output=True, text=True, check=True
        )
        token = result.stdout.strip()
        if token:
            return token
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        # az CLI not found or returned non-zero — try fallback
        pass

    token = os.environ.get("AZURE_ACCESS_TOKEN")
    if token:
        return token

    raise RuntimeError("No Azure credentials available. Run 'az login' or set AZURE_ACCESS_TOKEN.")
