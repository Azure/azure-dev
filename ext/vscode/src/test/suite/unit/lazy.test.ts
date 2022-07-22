// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { assert } from 'chai';
import { delay } from '../../testUtil/async';
import { AsyncLazy } from '../../../utils/lazy';

suite('lazy creation utilities', () => {
    suite('AsyncLazy tests', () => {
        test('factory called once', async () => {
            let factoryCallCount = 0;
            const lazy: AsyncLazy<boolean> = new AsyncLazy(async () => {
                factoryCallCount++;
                await delay(5);
                return true;
            });

            await lazy.getValue();
            await lazy.getValue();

            assert.equal(factoryCallCount, 1, 'Incorrect number of value factory calls.');
        });

        test('simultaneous callers', async () => {
            let factoryCallCount = 0;
            const lazy: AsyncLazy<boolean> = new AsyncLazy(async () => {
                factoryCallCount++;
                await delay(5);
                return true;
            });

            const p1 = lazy.getValue();
            const p2 = lazy.getValue();
            await Promise.all([p1, p2]);

            assert.equal(factoryCallCount, 1, 'Incorrect number of value factory calls.');
        });

        test('with lifetime', async () => {
            let factoryCallCount = 0;
            const lazy: AsyncLazy<boolean> = new AsyncLazy(async () => {
                factoryCallCount++;
                await delay(5);
                return true;
            }, 20);

            await lazy.getValue();
            await lazy.getValue();

            assert.equal(factoryCallCount, 1, 'Incorrect number of value factory calls.');

            await delay(50);
            await lazy.getValue();
            await lazy.getValue();

            assert.equal(factoryCallCount, 2, 'Incorrect number of value factory calls.');
        });

        test('simultaneous callers with lifetime', async () => {
            let factoryCallCount = 0;
            const lazy: AsyncLazy<boolean> = new AsyncLazy(async () => {
                factoryCallCount++;
                await delay(5);
                return true;
            }, 20);

            const p1 = lazy.getValue();
            const p2 = lazy.getValue();
            await Promise.all([p1, p2]);

            assert.equal(factoryCallCount, 1, 'Incorrect number of value factory calls.');

            await delay(50);
            const p3 = lazy.getValue();
            const p4 = lazy.getValue();
            await Promise.all([p3, p4]);

            assert.equal(factoryCallCount, 2, 'Incorrect number of value factory calls.');
        });
    });
});
