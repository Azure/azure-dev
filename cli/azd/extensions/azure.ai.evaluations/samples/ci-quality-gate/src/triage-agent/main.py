# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Hosted triage agent for the evaluation quality-gate sample.

Deliberately small. The point of the sample is the gate, not the agent -- swap
this for your own and the workflow is unchanged.

It is a *server*, not a script. `azure.yaml` declares this service as
`kind: hosted` with `startupCommand: python main.py`, so the container has to
keep serving the Responses protocol that the evaluation invokes per row. A
process that creates an agent and exits leaves nothing for the run to call.
"""

import os

from agent_framework import Agent
from agent_framework.foundry import FoundryChatClient
from agent_framework_foundry_hosting import ResponsesHostServer
from azure.identity import DefaultAzureCredential

INSTRUCTIONS = """You triage inbound customer support tickets.

Classify each message into exactly one queue: Order Status, Billing, Technical
Support, Account Management, Security, or General. Then answer the customer
directly and briefly.

Do not promise refunds, credits, or delivery dates you cannot confirm. If a
message suggests account compromise, treat it as urgent and say so.
"""


def main() -> None:
    # Both names are read because the two are set by different things: azd's
    # own service binding, and a locally exported environment. Failing with the
    # names rather than a KeyError is what makes a misconfigured container
    # diagnosable from its logs.
    model = os.getenv("AZURE_AI_MODEL_DEPLOYMENT_NAME") or os.getenv("FOUNDRY_MODEL_NAME")
    if not model:
        raise RuntimeError(
            "Model deployment name is not configured. Set "
            "AZURE_AI_MODEL_DEPLOYMENT_NAME or FOUNDRY_MODEL_NAME."
        )

    endpoint = os.getenv("FOUNDRY_PROJECT_ENDPOINT") or os.getenv("AZURE_AI_PROJECT_ENDPOINT")
    if not endpoint:
        raise RuntimeError(
            "Project endpoint is not configured. Set "
            "FOUNDRY_PROJECT_ENDPOINT or AZURE_AI_PROJECT_ENDPOINT."
        )

    client = FoundryChatClient(
        project_endpoint=endpoint,
        model=model,
        credential=DefaultAzureCredential(),
    )
    agent = Agent(
        client=client,
        instructions=INSTRUCTIONS,
        # The evaluation grades the answer, not a stored thread, and storing
        # every graded row costs the project state nobody reads.
        default_options={"store": False},
    )

    # Blocks. This is the whole difference between a hosted agent and a script.
    ResponsesHostServer(agent).run()


if __name__ == "__main__":
    main()
