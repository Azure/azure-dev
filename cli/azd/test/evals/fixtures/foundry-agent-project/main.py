import os

from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()

QUEUES = ["billing", "technical", "account"]


class Ticket(BaseModel):
    subject: str
    body: str


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/triage")
def triage(ticket: Ticket) -> dict[str, str]:
    """Route a ticket to a queue. Placeholder logic -- the real agent calls the model."""
    endpoint = os.environ.get("FOUNDRY_PROJECT_ENDPOINT", "")
    queue = QUEUES[len(ticket.subject) % len(QUEUES)]
    return {"queue": queue, "endpoint_configured": str(bool(endpoint))}
