"""Synthetic, model-free hosted fixture for task inspection (never deletes tasks)."""

import asyncio
import logging
import re

from starlette.requests import Request
from starlette.responses import JSONResponse

from azure.ai.agentserver.core.tasks import (
    TaskConflictError,
    TaskContext,
    multi_turn_task,
    set_resilient_tasks_enabled,
)
from azure.ai.agentserver.invocations import InvocationAgentServerHost

set_resilient_tasks_enabled(True)
app = InvocationAgentServerHost()
logger = logging.getLogger("tasks_fixture")
_pending: set[asyncio.Task] = set()


@multi_turn_task(name="act_tasks_inspection_fixture")
async def fixture_task(ctx: TaskContext[dict]) -> dict:
    """Stay observable in_progress, then suspend without removing the record."""
    logger.info("fixture running task_id=%s input_id=%s", ctx.task_id, ctx.input_id)
    await asyncio.sleep(ctx.input["delay_seconds"])
    logger.info("fixture returning task_id=%s input_id=%s", ctx.task_id, ctx.input_id)
    return {"task_id": ctx.task_id, "input_id": ctx.input_id, "synthetic": True}


async def consume_result(run):
    try:
        logger.info("fixture result=%s", await run.result())
    except Exception:
        logger.exception("fixture task failed task_id=%s", run.task_id)


@app.invoke_handler
async def invoke(request: Request):
    try:
        data = await request.json()
    except (ValueError, UnicodeDecodeError):
        return JSONResponse({"error": "expected JSON object"}, status_code=400)
    if not isinstance(data, dict):
        return JSONResponse({"error": "expected JSON object"}, status_code=400)
    for key in ("task_id", "input_id"):
        value = data.get(key)
        if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", value):
            return JSONResponse({"error": f"invalid {key}"}, status_code=400)
    delay = data.get("delay_seconds", 60)
    if type(delay) is not int or not 1 <= delay <= 180:
        return JSONResponse({"error": "delay_seconds must be an integer from 1 to 180"}, status_code=400)
    try:
        run = await fixture_task.start(
            task_id=data["task_id"],
            input_id=data["input_id"],
            input={
                "delay_seconds": delay,
                "synthetic": True,
                "call_id": request.state.call_id,
            },
        )
    except TaskConflictError:
        return JSONResponse({"error": "task already running", "task_id": data["task_id"]}, status_code=409)
    pending = asyncio.create_task(consume_result(run))
    _pending.add(pending)
    pending.add_done_callback(_pending.discard)
    return JSONResponse(
        {"task_id": run.task_id, "input_id": run.input_id, "synthetic": True},
        status_code=202,
    )


if __name__ == "__main__":
    app.run()
