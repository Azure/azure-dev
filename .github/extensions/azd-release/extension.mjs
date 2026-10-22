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
        description: "Track azd core releases using a private GitHub issue and live Azure DevOps evidence.",
        inputSchema: {
            type: "object",
            properties: { issueNumber: { type: "integer", minimum: 1 } },
            additionalProperties: false,
        },
        actions: [
            {
                name: "check_packages",
                description: "Explicitly check all package-readiness gates for the selected release before advertising a new version. Read-only except refreshing the local WinGet source.",
                handler: (ctx) => service.checkPackages({ issueNumber: panel(ctx.instanceId).selection.issueNumber }),
            },
            {
                name: "refresh",
                description: "Check authentication and read the release roadmap, pipeline evidence, and known previous issues.",
                handler: (ctx) => service.snapshot(panel(ctx.instanceId).selection.issueNumber),
            },
            {
                name: "analyze_failure",
                description: "Return a read-only investigation prompt for the selected release's failed jobs. Does not queue an agent turn.",
                handler: async (ctx) => service.diagnosis(panel(ctx.instanceId).selection.issueNumber),
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
