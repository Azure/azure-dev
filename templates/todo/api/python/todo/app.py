import motor
from azure.monitor.opentelemetry.exporter import AzureMonitorTraceExporter
from beanie import init_beanie
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.sdk.resources import SERVICE_NAME, Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
import os
from pathlib import Path

# CORS origins
apiUrl = os.environ.get('REACT_APP_WEB_BASE_URL')
if apiUrl is not None:
    origins = ["https://portal.azure.com",
               "https://ms.portal.azure.com",
               "http://localhost:3000/",
               apiUrl]
    print("CORS with", origins[2] , "is allowed for local host debugging. If you want to change pin number, go to", Path(__file__))
else:
    origins = ["*"]
    print("Setting CORS to allow all origins because env var REACT_APP_WEB_BASE_URL has no value or is not set.")

from .models import Settings, __beanie_models__

settings = Settings()
app = FastAPI(
    description="Simple Todo API",
    version="2.0.0",
    title="Simple Todo API",
    docs_url="/",
)
app.add_middleware(
    CORSMiddleware,
    allow_origins=origins,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

from . import routes  # NOQA


@app.on_event("startup")
async def startup_event():
    if settings.APPLICATIONINSIGHTS_CONNECTION_STRING:
        exporter = AzureMonitorTraceExporter.from_connection_string(
            settings.APPLICATIONINSIGHTS_CONNECTION_STRING
        )
        tracer = TracerProvider(
            resource=Resource({SERVICE_NAME: settings.APPLICATIONINSIGHTS_ROLENAME})
        )
        tracer.add_span_processor(BatchSpanProcessor(exporter))

        FastAPIInstrumentor.instrument_app(app, tracer_provider=tracer)

    client = motor.motor_asyncio.AsyncIOMotorClient(
        settings.AZURE_COSMOS_CONNECTION_STRING
    )
    await init_beanie(
        database=client[settings.AZURE_COSMOS_DATABASE_NAME],
        document_models=__beanie_models__,
    )
