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

# For Azure services which don't support setting CORS directly within the service (like Static Web Apps)
# You can enable localhost cors access if you want to allow request from a localhost here.
#    example: const localhostOrigin = "http://localhost:3000/";
# Keep empty string to deny localhost origin.
localhostOrigin = ""

# CORS origins
# env.ENABLE_ORYX_BUILD is only set on Azure environment during azd provision for todo-templates
# You can update this to env.NODE_ENV in your app is using `development` to run locally and another value
# when the app is running on Azure (like production or stating)
runningOnAzure = os.environ.get('ENABLE_ORYX_BUILD')
if runningOnAzure is not None:
    origins = [
        "https://portal.azure.com",
        "https://ms.portal.azure.com",
    ]
    
    # REACT_APP_WEB_BASE_URL must be set for the api service as a property
    # otherwise the api server will reject the origin.
    apiUrlSet = os.environ.get('REACT_APP_WEB_BASE_URL')
    if apiUrlSet is not None:
        origins.append(apiUrlSet)
    
    if localhostOrigin is not None:
        origins.append(localhostOrigin)
        print("Allowing requests from", localhostOrigin, ". To change or disable, go to ", Path(__file__))
    
else:
    origins = ["*"]
    print("Allowing requests from any origin because the server is running locally.")

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
