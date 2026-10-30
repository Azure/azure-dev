// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { createServer } from "node:http";
import { randomBytes } from "node:crypto";
import { readFile } from "node:fs/promises";
import { requireValue, positiveInt } from "./model.mjs";
import { errorMessage, ReleaseError } from "./cli.mjs";

const assets = new Map([
    ["", ["index.html", "text/html; charset=utf-8"]],
    ["app.js", ["app.js", "text/javascript; charset=utf-8"]],
    ["style.css", ["style.css", "text/css; charset=utf-8"]],
]);
const fields = {
    create: ["releaseVersion"],
    "prepare-changelog": ["issueNumber", "mode"],
    "link-changelog": ["issueNumber", "changelogPr"],
    adopt: ["issueNumber", "runId"],
    "prepare-queue": ["issueNumber", "recordMode"],
    queue: ["issueNumber", "ticket", "confirmation"],
    milestone: ["issueNumber", "key", "status", "evidence"],
    complete: ["issueNumber", "confirmation"],
    analyze: ["issueNumber"],
    "check-packages": ["issueNumber"],
    "refresh-live": ["issueNumber"],
    select: ["issueNumber"],
};

async function readJson(request) {
    let body = "";
    for await (const chunk of request) {
        body += chunk;
        requireValue(body.length <= 16_384, "Request body is too large.");
    }
    let data;
    try { data = JSON.parse(body); }
    catch { throw new ReleaseError("validation", "Expected a JSON request body."); }
    requireValue(data && typeof data === "object" && !Array.isArray(data), "Expected a JSON object.");
    return data;
}

export async function startServer(service, session, issueNumber) {
    const selection = { issueNumber };
    const snapshot = async () => {
        const selected = selection.issueNumber;
        const state = await service.snapshot(selected);
        if (state.access?.github.ready && !state.restricted && state.release &&
            selection.issueNumber === selected) {
            selection.issueNumber = state.release.issueNumber;
        }
        return state;
    };
    const token = randomBytes(32).toString("hex");
    const prefix = `/${token}/`;
    let origin;
    const server = createServer(async (request, response) => {
        response.setHeader("Cache-Control", "no-store");
        response.setHeader("Referrer-Policy", "no-referrer");
        response.setHeader("X-Content-Type-Options", "nosniff");
        response.setHeader("Content-Security-Policy",
            "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'");
        const json = (status, data) => {
            response.writeHead(status, { "Content-Type": "application/json; charset=utf-8" });
            response.end(JSON.stringify(data));
        };
        try {
            requireValue(request.headers.host === new URL(origin).host, "Invalid request host.");
            requireValue(!request.headers.origin || request.headers.origin === origin, "Cross-origin requests are not allowed.");
            const path = new URL(request.url, origin).pathname;
            if (!path.startsWith(prefix)) { json(404, { error: "Not found." }); return; }
            const route = path.slice(prefix.length);
            if (request.method === "GET" && assets.has(route)) {
                const [name, type] = assets.get(route);
                const content = await readFile(new URL(name, import.meta.url));
                response.writeHead(200, { "Content-Type": type });
                response.end(content);
                return;
            }
            if (request.method === "GET" && route === "api/state") {
                json(200, await snapshot());
                return;
            }
            const action = route.startsWith("api/") ? route.slice(4) : "";
            requireValue(request.method === "POST" && Object.hasOwn(fields, action), "Unknown action.");
            requireValue(request.headers["x-azd-release-token"] === token &&
                request.headers["content-type"] === "application/json", "Invalid action request.");
            const data = await readJson(request);
            requireValue(Object.keys(data).every((key) => fields[action].includes(key)), "Unsupported action field.");
            let result;
            if (action === "select") {
                const selected = positiveInt(data.issueNumber, "Tracking issue");
                await service.requireAccess();
                selection.issueNumber = selected;
                result = { selected: selection.issueNumber };
            } else if (action === "prepare-changelog") {
                result = await service.prepareChangelog(data);
            } else if (action === "link-changelog") {
                result = await service.linkChangelog(data);
            } else if (action === "analyze") {
                const diagnosis = await service.diagnosis(data.issueNumber);
                await session.send({ prompt: diagnosis.prompt, mode: "immediate" });
                result = { requested: true };
            } else if (action === "prepare-queue") {
                result = await service.prepareQueue(data.issueNumber, data.recordMode);
            } else if (action === "check-packages") {
                result = await service.checkPackages(data);
            } else if (action === "refresh-live") {
                result = await service.refreshLive(data);
            } else {
                result = await service[action](data);
                if (action === "create") selection.issueNumber = result.issueNumber;
            }
            json(200, result);
        } catch (error) {
            json(error.code === "access_required" ? 403 : 400,
                { error: errorMessage(error), code: error instanceof ReleaseError ? error.code : "request_failed" });
        }
    });
    await new Promise((resolve, reject) => {
        server.once("error", reject);
        server.listen(0, "127.0.0.1", resolve);
    });
    origin = `http://127.0.0.1:${server.address().port}`;
    return {
        server, selection, snapshot, url: `${origin}${prefix}`,
        selectedIssueNumber: async () => {
            if (selection.issueNumber === undefined) await snapshot();
            return selection.issueNumber;
        },
        close: () => new Promise((resolve, reject) => {
            server.close((error) => error ? reject(error) : resolve());
            server.closeAllConnections();
        }),
    };
}
