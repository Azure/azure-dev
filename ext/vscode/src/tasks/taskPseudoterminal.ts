// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

import * as vscode from 'vscode';
import { PseudoterminalWriter } from './pseudoterminalWriter';

// The optional return value is treated as "exit code" (nonzero value means failure).
export type TaskRun = (writer: PseudoterminalWriter, cts: vscode.CancellationToken) => Promise<number | void>;

export class TaskPseudoterminal extends vscode.Disposable implements vscode.Pseudoterminal {
    private readonly closeEmitter: vscode.EventEmitter<number | void> = new vscode.EventEmitter<number | void>();
    private readonly writeEmitter: vscode.EventEmitter<string> = new vscode.EventEmitter<string>();
    private readonly cts: vscode.CancellationTokenSource = new vscode.CancellationTokenSource();

    constructor(private readonly runTask: TaskRun) {
        super(
            () => {
                this.close();

                this.closeEmitter.dispose();
                this.writeEmitter.dispose();
                this.cts.dispose();
            });
    }

    readonly onDidClose: vscode.Event<number | void> = this.closeEmitter.event;
    public readonly onDidWrite: vscode.Event<string> = this.writeEmitter.event;

    open(): void {
        this.runTask(
            new PseudoterminalWriter(
                (output: string) => {
                    this.writeEmitter.fire(output);
                }),
            this.cts.token)
            .then(value => this.closeWithValue(value))
            .catch(() => this.closeWithValue());
    }

    close(): void {
        this.closeWithValue();
    }

    private closeWithValue(value: number | void): void {
        this.cts.cancel();
        this.closeEmitter.fire(value);
    }
}
