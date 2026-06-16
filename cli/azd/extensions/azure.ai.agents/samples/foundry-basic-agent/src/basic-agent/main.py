"""Minimal hosted agent entry point for the Foundry unified azure.yaml POC.

azd packages this directory (the agent's `project:` in azure.yaml) and Foundry
runs `startupCommand` (`python main.py`) as the code-deploy entry point. Replace
the handler below with your agent logic; this stub only proves the end-to-end
package -> deploy flow for a single hosted agent.
"""

import os


def main() -> None:
    deployment = os.environ.get("FOUNDRY_MODEL_DEPLOYMENT_NAME", "gpt-4o-mini")
    endpoint = os.environ.get("FOUNDRY_PROJECT_ENDPOINT", "<resolved-at-deploy>")
    print(f"basic-agent starting (model deployment: {deployment}, project: {endpoint})")
    # TODO: implement request handling for the `responses` protocol declared in azure.yaml.


if __name__ == "__main__":
    main()
