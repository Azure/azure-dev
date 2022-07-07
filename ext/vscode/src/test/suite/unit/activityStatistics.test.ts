// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as dayjs from 'dayjs';
import { assert } from 'chai';

import { ActivityStatisticsService, ActivityStats, TelemetryEnablement } from '../../../telemetry/activityStatisticsService';
import { TestMemento } from '../../testUtil/TestMemento';


suite('activity statistics', () => {
    test('reports zero days for new user', () => {
        const statSrv = new ActivityStatisticsService(new TestMemento(), TelemetryEnablement.NotRequired);

        const stats = statSrv.getStats();

        assert.equal(stats.lastKnownActivityDay, undefined);
        assert.equal(stats.totalActiveDays, 0);
    });

    test('multiple activities on the same day increment active days count only once', async () => {
        const statSrv = new ActivityStatisticsService(new TestMemento(), TelemetryEnablement.NotRequired);

        await statSrv.recordActivity();
        await statSrv.recordActivity();
        const stats = statSrv.getStats();

        assert.isTrue(dayjs().isSame(stats.lastKnownActivityDay, 'day'));
        assert.equal(stats.totalActiveDays, 1);
    });

    test('activity on different days increment active day count', async () => {
        const globalStore = new TestMemento();
        const startingStats: ActivityStats = {
            lastKnownActivityDay: dayjs().subtract(2, 'day').valueOf(),
            totalActiveDays: 2
        };
        await globalStore.update('vscode:/extensions/ms-azuretools.azure-dev/activity/overall', startingStats);
        const statSrv = new ActivityStatisticsService(globalStore, TelemetryEnablement.NotRequired);


        await statSrv.recordActivity();
        const stats = statSrv.getStats();

        assert.isTrue(dayjs().isSame(stats.lastKnownActivityDay, 'day'));
        assert.equal(stats.totalActiveDays, 3);
    });
});
