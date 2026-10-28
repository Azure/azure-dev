// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { createCanvas, joinSession, CanvasError } from "@github/copilot-sdk/extension";
import { ReleaseService } from "./release-service.mjs";
import { startServer } from "./server.mjs";
import { ChangelogWorkflow } from "./changelog.mjs";

const panels = new Map();
const service = new ReleaseService();

function panel(instanceId) {
    const entry = panels.get(instanceId);
    if (!entry) throw new CanvasError("panel_closed", "Open the release canvas first.");
    return entry;
}

const session = await joinSession({
    canvases: [createCanvas({
        id: "azd-release",
        displayName: "azd release",
        description: "Follow shared azd release snapshots from a private issue, with explicit live refresh and deliberate release actions.",
        inputSchema: {
            type: "object",
            properties: { issueNumber: { type: "integer", minimum: 1 } },
            additionalProperties: false,
        },
        actions: [
            {
                name: "check_packages",
                description: "Check package readiness with fresh Azure verification. Refreshes local WinGet; the driver saves sanitized observations in the private issue, while observers' checks stay local. No publishing.",
                handler: async (ctx) => service.checkPackages({
                    issueNumber: await panel(ctx.instanceId).selectedIssueNumber(),
                }),
            },
            {
                name: "refresh",
                description: "Verify private GitHub access and load saved release observations. Does not query Azure, recheck public packages, or write tracking.",
                handler: (ctx) => panel(ctx.instanceId).snapshot(),
            },
            {
                name: "refresh_live",
                description: "Explicitly refresh Azure DevOps and PR observations. The release driver saves sanitized snapshots to the private issue; observers get local results only. Does not publish or approve anything.",
                handler: async (ctx) => service.refreshLive({
                    issueNumber: await panel(ctx.instanceId).selectedIssueNumber(),
                }),
            },
            {
                name: "analyze_failure",
                description: "Return a read-only investigation prompt for the selected release's failed jobs. Does not queue an agent turn.",
                handler: async (ctx) => service.diagnosis(await panel(ctx.instanceId).selectedIssueNumber()),
            },
        ],
        open: async (ctx) => {
            service.changelog ??= new ChangelogWorkflow(session);
            let entry = panels.get(ctx.instanceId);
            if (!entry) {
                entry = await startServer(service, session, ctx.input?.issueNumber);
                panels.set(ctx.instanceId, entry);
            }
            return { title: "azd release", url: entry.url };
        },
        onClose: async (ctx) => {
            const entry = panels.get(ctx.instanceId);
            if (entry) {
                panels.delete(ctx.instanceId);
                await entry.close();
            }
        },
    })],
});
