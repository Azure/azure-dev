// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

export function withTimeout<T>(promise: Promise<T>, timeout: number, message?: string): Promise<T> {
    message ??= vscode.l10n.t('Timed out waiting for a task to complete.');

    return new Promise((resolve, reject) => {
        const timer = setTimeout(
            () => reject(new Error(message)),
            timeout);

        promise.then(
            (result) => {
                clearTimeout(timer);

                return resolve(result);
            },
            (err) => {
                clearTimeout(timer);

                return reject(err);
            }
        );
    });
}
