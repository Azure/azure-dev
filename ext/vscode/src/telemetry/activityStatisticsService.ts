// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import ext from '../ext';

export type ActivityKind = 'overall';

export type ActivityStats = {
    lastKnownActivityDay: number | undefined;
    totalActiveDays: number;
};

export enum TelemetryEnablement {
    Required = 0,
    NotRequired = 1
}

const zeroStats: ActivityStats = { lastKnownActivityDay: undefined, totalActiveDays: 0 };

const activityPrefix = `${ext.azureDevExtensionNamespace}/activity`;

export class ActivityStatisticsService {
    private readonly stats = new Map<ActivityKind, ActivityStats>();

    public constructor(
        private readonly persistentStore: vscode.Memento, 
        private readonly telemetryEnablement: TelemetryEnablement = TelemetryEnablement.Required
    ) {}

    public async recordActivity(kind: ActivityKind = 'overall'): Promise<unknown> {
        if (this.telemetryEnablement === TelemetryEnablement.Required && !vscode.env.isTelemetryEnabled) {
            return;
        }

        try {
            const currentStats = this.getStats(kind);
            const now = Date.now();

            if (sameDay(now, currentStats.lastKnownActivityDay)) {
                return;
            }

            const newStats: ActivityStats = {
                lastKnownActivityDay: now,
                totalActiveDays: currentStats.totalActiveDays + 1
            };
            this.stats.set(kind, newStats);

            // Await here, so if the caller wants to wait for the activity stats to be updated, they can.
            await this.persistentStore.update(`${activityPrefix}/${kind}`, newStats);

            return undefined;
        } catch(err: unknown) {
            // Best effort--don't fail the activity just because we weren't able to records stats for it.
            return err;
        }
    }

    public getStats(kind: ActivityKind = 'overall'): ActivityStats {
        if (this.telemetryEnablement === TelemetryEnablement.Required && !vscode.env.isTelemetryEnabled) {
            return zeroStats;
        }
        
        let currentStats = this.stats.get(kind);
        if (!currentStats) {
            currentStats = this.persistentStore.get<ActivityStats>(`${activityPrefix}/${kind}`, zeroStats);
            this.stats.set(kind, currentStats);
        }

        return currentStats;
    }
}

function sameDay(a: number | undefined, b: number | undefined): boolean {
    if (a === undefined || b === undefined) {
        return false;
    }

    const da = new Date(a);
    const db = new Date(b);

    const retval = da.getFullYear() === db.getFullYear() && da.getMonth() === db.getMonth() && da.getDay() === db.getDay();
    return retval;
}
