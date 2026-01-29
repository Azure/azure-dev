// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

/**
 * Shared FileSystemWatcher service to avoid creating multiple watchers.
 * FileSystemWatchers are a limited resource on some systems (only a few hundred available).
 */
export class FileSystemWatcherService implements vscode.Disposable {
    private watchers = new Map<string, { watcher: vscode.FileSystemWatcher; listeners: Set<(uri: vscode.Uri) => void> }>();

    /**
     * Watch a glob pattern for changes
     * @param pattern The glob pattern to watch
     * @param callback Callback to invoke when files matching the pattern change
     * @returns Disposable to stop watching
     */
    public watch(pattern: string, callback: (uri: vscode.Uri) => void): vscode.Disposable {
        let watcherEntry = this.watchers.get(pattern);

        if (!watcherEntry) {
            // Create new watcher and entry for this pattern
            const watcher = vscode.workspace.createFileSystemWatcher(pattern);
            watcherEntry = {
                watcher,
                listeners: new Set()
            };
            this.watchers.set(pattern, watcherEntry);

            // Set up forwarding events to all listeners
            // Note: watcherEntry is captured in closure and will include all future listeners
            watcher.onDidChange(uri => {
                // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
                watcherEntry!.listeners.forEach(listener => listener(uri));
            });
            watcher.onDidCreate(uri => {
                // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
                watcherEntry!.listeners.forEach(listener => listener(uri));
            });
            watcher.onDidDelete(uri => {
                // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
                watcherEntry!.listeners.forEach(listener => listener(uri));
            });
        }

        // Add callback to listeners (whether entry is new or existing)
        watcherEntry.listeners.add(callback);

        return {
            dispose: () => {
                const entry = this.watchers.get(pattern);
                if (entry) {
                    entry.listeners.delete(callback);
                    if (entry.listeners.size === 0) {
                        entry.watcher.dispose();
                        this.watchers.delete(pattern);
                    }
                }
            }
        };
    }

    public dispose(): void {
        this.watchers.forEach(entry => void entry.watcher.dispose());
        this.watchers.clear();
    }
}
