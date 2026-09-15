import os

from fastapi import FastAPI

app = FastAPI()


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "log_level": os.environ.get("LOG_LEVEL", "warning")}
