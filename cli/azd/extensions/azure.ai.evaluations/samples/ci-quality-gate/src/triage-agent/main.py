# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Minimal hosted agent for the evaluation quality-gate sample.

Deliberately small. The point of the sample is the gate, not the agent -- swap
this for your own and the workflow is unchanged.
"""

import os

from azure.ai.agents import AgentsClient
from azure.identity import DefaultAzureCredential

INSTRUCTIONS = """You triage inbound customer support tickets.

Classify each message into exactly one queue: Order Status, Billing, Technical
Support, Account Management, Security, or General. Then answer the customer
directly and briefly.

Do not promise refunds, credits, or delivery dates you cannot confirm. If a
message suggests account compromise, treat it as urgent and say so.
"""


def main() -> None:
    endpoint = os.environ["AZURE_AI_PROJECT_ENDPOINT"]
    model = os.environ.get("AZURE_AI_MODEL_DEPLOYMENT_NAME", "gpt-4o-mini")

    client = AgentsClient(endpoint=endpoint, credential=DefaultAzureCredential())
    client.create_agent(
        model=model,
        name="triage-agent",
        instructions=INSTRUCTIONS,
    )


if __name__ == "__main__":
    main()
