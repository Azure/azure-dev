from fastapi import FastAPI
from fastapi.responses import PlainTextResponse
from util.text import get_text

app = FastAPI()

@app.get("/")
async def index():
    return PlainTextResponse(content=get_text())

if __name__ == '__main__':
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)