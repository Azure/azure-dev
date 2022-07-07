import azure.functions as func
from azure.functions._http_asgi import AsgiResponse, AsgiRequest
from todo import app  # Main API application

initialized = False

async def ensure_init(app):
    global initialized
    if not initialized:
        await app.startup_event()
        initialized = True

async def handle_asgi_request(req: func.HttpRequest, context: func.Context) -> func.HttpResponse:
    asgi_request = AsgiRequest(req, context)
    scope = asgi_request.to_asgi_http_scope()
    asgi_response = await AsgiResponse.from_app(app.app, scope, req.get_body())
    return asgi_response.to_func_response()

async def main(req: func.HttpRequest, context: func.Context) -> func.HttpResponse:
    await ensure_init(app)
    return await handle_asgi_request(req, context)
