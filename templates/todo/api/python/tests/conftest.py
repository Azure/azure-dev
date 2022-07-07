import asyncio

import motor
import pytest
from fastapi.testclient import TestClient
from todo.app import app, settings

TEST_DB_NAME = "test_db"


@pytest.fixture(scope="session")
def event_loop():
    """
    Redefine the event_loop fixture to be session scoped.
    Requirement of pytest-asyncio if there are async fixtures
    with non-function scope.
    """
    try:
        return asyncio.get_running_loop()
    except RuntimeError:
        return asyncio.new_event_loop()


@pytest.fixture()
def app_client():
    with TestClient(app) as client:
        yield client


@pytest.fixture(scope="session", autouse=True)
async def initialize_database():
    settings.AZURE_COSMOS_DATABASE_NAME = TEST_DB_NAME
    mongo_client = motor.motor_asyncio.AsyncIOMotorClient(
        settings.AZURE_COSMOS_CONNECTION_STRING
    )
    await mongo_client.drop_database(TEST_DB_NAME)
    yield
    await mongo_client.drop_database(TEST_DB_NAME)
