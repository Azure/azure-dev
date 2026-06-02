"""Minimal agent source fixture for `azd ai agent init --from-code` scenarios.

This file exists only so the extension's from-code detection treats the working
directory as a Python agent project (it looks for requirements.txt or any .py
file, and uses app.py as the default entry point). The init flow scaffolds an
agent.yaml around this code; the body does not need to be a fully functional
agent for the scaffold-only Tier 1 scenarios.
"""


def handler(request: str) -> str:
    """Echo the incoming request back to the caller."""
    return f"echo: {request}"


if __name__ == "__main__":
    print(handler("hello"))
