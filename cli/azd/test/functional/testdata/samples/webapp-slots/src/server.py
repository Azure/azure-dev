#!/usr/bin/env python3
"""Simple Flask app that returns the contents of data.json."""

import json
import os
from flask import Flask, Response

app = Flask(__name__)

@app.route("/")
@app.route("/<path:path>")
def index(path=""):
    # Get the directory where this script is located
    script_dir = os.path.dirname(os.path.abspath(__file__))
    data_file = os.path.join(script_dir, "data.json")
    
    try:
        with open(data_file, "r") as f:
            data = f.read()
        return Response(data, mimetype="application/json")
    except FileNotFoundError:
        return Response(
            json.dumps({"error": "data.json not found"}),
            status=404,
            mimetype="application/json"
        )

if __name__ == "__main__":
    port = int(os.environ.get("PORT", os.environ.get("WEBSITES_PORT", 8000)))
    app.run(host="0.0.0.0", port=port)
