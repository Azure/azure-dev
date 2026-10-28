# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.
"""Deterministic native A2A 1.0 fixture; no model, credentials, or external calls."""

import os

import uvicorn
from a2a.helpers import new_task_from_user_message, new_text_message, new_text_part
from a2a.server.agent_execution import AgentExecutor, RequestContext
from a2a.server.events import EventQueue
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.routes import create_agent_card_routes, create_jsonrpc_routes
from a2a.server.tasks import InMemoryTaskStore, TaskUpdater
from a2a.types import AgentCapabilities, AgentCard, AgentInterface, AgentSkill
from a2a.types.a2a_pb2 import TaskState
from starlette.applications import Starlette
from starlette.responses import PlainTextResponse
from starlette.routing import Route

NATIVE_MARKER = "AZD_A2A_NATIVE_ECHO_OK"


class EchoExecutor(AgentExecutor):
    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        task = context.current_task
        if task is None:
            task = new_task_from_user_message(context.message)
            await event_queue.enqueue_event(task)
        updater = TaskUpdater(event_queue=event_queue, task_id=task.id, context_id=task.context_id)
        await updater.update_status(state=TaskState.TASK_STATE_WORKING)
        # Echo a fixed answer rather than request text: the marker proves execution,
        # not merely that a service returned the input in task history.
        await updater.add_artifact(parts=[new_text_part(text=NATIVE_MARKER, media_type="text/plain")])
        await updater.update_status(
            state=TaskState.TASK_STATE_COMPLETED, message=new_text_message("Done")
        )

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        raise NotImplementedError("Cancellation is not used by this retained-resource fixture")


async def health(_request):
    return PlainTextResponse("ok")


def build_app(port: int) -> Starlette:
    hostname = os.getenv("CONTAINER_APP_HOSTNAME")
    url = f"https://{hostname}" if hostname else f"http://localhost:{port}/"
    card = AgentCard(
        name="azd native A2A echo",
        description=NATIVE_MARKER,
        version="1.0.0",
        default_input_modes=["text/plain"],
        default_output_modes=["text/plain"],
        capabilities=AgentCapabilities(streaming=False),
        supported_interfaces=[AgentInterface(protocol_binding="JSONRPC", protocol_version="1.0", url=url)],
        skills=[AgentSkill(
            id="echo", name="Deterministic echo", description="Returns a fixed execution marker",
            tags=["test"], input_modes=["text/plain"], output_modes=["text/plain"],
        )],
    )
    handler = DefaultRequestHandler(
        agent_executor=EchoExecutor(), task_store=InMemoryTaskStore(), agent_card=card
    )
    routes = []
    # Hosted runtime routing varies; keep current and older aliases available.
    for path in ("/a2a", "/", "/invocations/a2a"):
        routes.extend(create_jsonrpc_routes(handler, path))
    for path in ("/.well-known/agent-card.json", "/agent_card",
                 "/a2a/.well-known/agent-card.json", "/invocations/a2a/.well-known/agent-card.json"):
        routes.extend(create_agent_card_routes(card, card_url=path))
    for path in ("/health", "/readiness"):
        routes.append(Route(path, health, methods=["GET"]))
    return Starlette(routes=routes)


if __name__ == "__main__":
    port = int(os.getenv("PORT", "8080"))
    uvicorn.run(build_app(port), host="0.0.0.0", port=port)
