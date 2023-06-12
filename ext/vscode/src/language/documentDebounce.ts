// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Adapted from https://github.com/microsoft/compose-language-service/blob/main/src/service/utils/debounce.ts with slight changes

import * as vscode from 'vscode';

export interface DebounceId {
    uri: vscode.Uri;
    callId: string;
}

const activeDebounces: { [key: string]: vscode.Disposable } = {};

export function documentDebounce(delay: number, id: DebounceId, callback: () => Promise<void> | void, thisArg?: unknown): void {
    const idString = `${id.uri}/${id.callId}`;

    // If there's an existing call queued up, wipe it out (can't simply refresh as the inputs to the callback may be different)
    if (activeDebounces[idString]) {
        activeDebounces[idString].dispose();
        delete activeDebounces[idString];
    }

    // Schedule the callback
    const timeout = setTimeout(() => {
        // Clear the callback since we're about to fire it
        activeDebounces[idString].dispose();
        delete activeDebounces[idString];

        // Fire it
        void callback.call(thisArg);
    }, delay);

    // Keep track of the active debounce
    activeDebounces[idString] = {
        dispose: () => clearTimeout(timeout),
    };
}
