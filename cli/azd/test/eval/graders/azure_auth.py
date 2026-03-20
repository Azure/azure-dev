"""Shared Azure authentication helper for eval graders."""
import os


def get_access_token() -> str:
    """Get Azure access token using Azure CLI or environment variable.

    Tries `az account get-access-token` first, then falls back to
    the AZURE_ACCESS_TOKEN environment variable.
    """
    try:
        import subprocess
        result = subprocess.run(
            ["az", "account", "get-access-token", "--query", "accessToken", "-o", "tsv"],
            capture_output=True, text=True, check=True
        )
        return result.stdout.strip()
    except Exception:
        pass

    token = os.environ.get("AZURE_ACCESS_TOKEN")
    if token:
        return token

    raise RuntimeError("No Azure credentials available. Run 'az login' or set AZURE_ACCESS_TOKEN.")
