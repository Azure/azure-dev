// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import * as dotenv from 'dotenv';
import * as dayjs from 'dayjs';
import * as duration from 'dayjs/plugin/duration';
dayjs.extend(duration);
import { callWithTelemetryAndErrorHandling, IActionContext } from '@microsoft/vscode-azext-utils';
import { localize } from '../localize';
import { TaskPseudoterminal } from './taskPseudoterminal';
import { PseudoterminalWriter } from './pseudoterminalWriter';
import { resolveVariables } from '../utils/resolveVariables';
import { TelemetryId } from '../telemetry/telemetryId';
import ext from '../ext';
import { fileExists } from '../utils/fileUtils';

interface DotEnvTaskDefinition extends vscode.TaskDefinition {
    file: string;
    targetTasks: string | string[];
}

export enum DotEnvTaskResult {
    Succeeded = 0,
    ErrorReferenceToSelf = 1,
    ErrorEnvFileDoesNotExist = 2,
    ErrorTargetTaskNotFound = 3,
    ErrorTaskTypeNotSupported = 4,
    ErrorChildTaskFailed = 5,
    Cancelled = 6
}

export class DotEnvTaskProvider implements vscode.TaskProvider {
    public provideTasks(token: vscode.CancellationToken): vscode.ProviderResult<vscode.Task[]> {
        return [];
    }

    public resolveTask(task: vscode.Task, token: vscode.CancellationToken): vscode.Task {
        const resolved = new vscode.Task(
            task.definition,
            task.scope || vscode.TaskScope.Workspace,
            task.name,
            task.source,
            new vscode.CustomExecution((resolvedDefinition: vscode.TaskDefinition) => Promise.resolve(
                new TaskPseudoterminal((writer, ct) => this.executeTaskWithTelemetry(task.name, resolvedDefinition as DotEnvTaskDefinition, writer, ct))
            )),
            task.problemMatchers
        );
        resolved.presentationOptions.reveal = vscode.TaskRevealKind.Silent;
        return resolved;
    }

    private executeTaskWithTelemetry(
        taskName: string, 
        resolvedDefinition: DotEnvTaskDefinition, 
        writer: PseudoterminalWriter, 
        ct: vscode.CancellationToken
    ): Promise<number> {
        return callWithTelemetryAndErrorHandling(TelemetryId.DotEnvTask, async (context: IActionContext) => {
            context.errorHandling.suppressDisplay = true; // Errors will be shown in the task pane
            context.errorHandling.rethrow = true;

            const result = await this.executeTask(context, taskName, resolvedDefinition, writer, ct);

            if (result === DotEnvTaskResult.Cancelled) {
                context.telemetry.properties.result = 'Canceled';
            } else if (result !== DotEnvTaskResult.Succeeded) {
                context.telemetry.properties.result = 'Failed';
                context.telemetry.properties.error = DotEnvTaskResult[result];
            }

            void ext.activitySvc.recordActivity();

            return result;
        }) as Promise<number>;
    }

    private async executeTask(
        context: IActionContext, 
        dotEnvTaskName: string, 
        resolvedDefinition: DotEnvTaskDefinition, 
        writer: PseudoterminalWriter, 
        ct: vscode.CancellationToken
    ): Promise<DotEnvTaskResult> {
        const resolvedFile = vscode.Uri.file(resolveVariables(resolvedDefinition.file));
        if (!await fileExists(resolvedFile)) {
            writer.writeLine(localize('azure-dev.tasks.dotenv.envFileDoesNotExist', "Error: environment file '{0}' does not exist", resolvedFile.fsPath), 'bold');
            return DotEnvTaskResult.ErrorEnvFileDoesNotExist;
        }
        const envVars = await getEnvVars(resolvedFile);

        const candidates = await vscode.tasks.fetchTasks();
        const tasksToExecute: string[] = typeof resolvedDefinition.targetTasks === 'string' ? [ resolvedDefinition.targetTasks ] : resolvedDefinition.targetTasks;

        // This implementation executes target tasks sequentially, but can be easily modified to execute tasks in parallel
        // if such need arises in future.
        for (const targetTaskName of tasksToExecute) {
            if (ct.isCancellationRequested) {
                writer.writeLine(localize('azure-dev.tasks.dotenv.taskCancelled', "The task was cancelled. Exiting..."));
                return DotEnvTaskResult.Cancelled;
            }

            if (targetTaskName === dotEnvTaskName) {
                writer.writeLine(localize('azure-dev.tasks.dotenv.cannotReferenceSelf', "Error: target task cannot be the same as the current task"), 'bold');
                return DotEnvTaskResult.ErrorReferenceToSelf;
            }

            const target = candidates.find(t => t.name === targetTaskName);
            if (target === undefined) {
                context.telemetry.properties.targetTaskName = targetTaskName;
                writer.writeLine(localize('azure-dev.tasks.dotenv.taskNotFound', "Error: target task '{0}' was not found", targetTaskName), 'bold');
                return DotEnvTaskResult.ErrorTargetTaskNotFound;
            }

            const result = await executeChildTask(context, target, envVars, writer);
            if (result !== DotEnvTaskResult.Succeeded) {
                return result;
            }
        }

        writer.writeLine(localize('azure-dev.tasks.dotenv.allTasksSucceeded', "All tasks succeeded, exiting..."));
        return DotEnvTaskResult.Succeeded;
    }
}

async function executeChildTask(
    context: IActionContext, 
    target: vscode.Task, 
    envVars: { [key: string]: string }, 
    writer: PseudoterminalWriter
): Promise<DotEnvTaskResult> {
    const haveExecution = target.execution && (target.execution instanceof vscode.ProcessExecution || target.execution instanceof vscode.ShellExecution);
    if (!haveExecution) {
        writer.writeLine(localize('azure-dev.tasks.dotenv.taskTypeNotSupported', "Error: target task '{0}' is of type that is not supported by dotenv task", target.name), 'bold');
        return DotEnvTaskResult.ErrorTaskTypeNotSupported;
    }

    const execution = target.execution as (vscode.ProcessExecution | vscode.ShellExecution);
    if (!execution.options) {
        execution.options = {
            env: {}
        };
    } else if (!execution.options.env) {
        execution.options.env = {};
    }

    /* eslint-disable @typescript-eslint/no-non-null-assertion */
    for (const varName in envVars) {
        // Do not override what is already in the task environment variable configuration
        if (! execution.options.env![varName]) {
            execution.options.env![varName] = envVars[varName];
        }
    }
    /* eslint-enable @typescript-eslint/no-non-null-assertion */

    // We have to invoke the execution property setter to make the changes effective.
    target.execution = execution;

    // Opt out of the VS Code logic that ensures tasks with the same command line have at most one instance running. 
    // Any constraints like this should apply to our "dotenv" task, and not to tasks launched by us.
    target.definition.idRandomizer = Math.random();
    const startTime = dayjs();
    
    const taskExecution = await vscode.tasks.executeTask(target);
    writer.writeLine(localize('azure-dev.tasks.dotenv.taskStarted', "Child task '{0}' started", target.name));

    const taskEndPromise = new Promise<number>((resolve) => {
        const disposable = vscode.tasks.onDidEndTaskProcess(e => {
            if (e.execution === taskExecution) {
                disposable.dispose();
                resolve(e.exitCode ?? 0);
            }
        });
    });

    const exitCode = await taskEndPromise;
    const endTime = dayjs();
    const duration = dayjs.duration(endTime.diff(startTime));

    if (exitCode !== 0) {
        writer.writeLine(localize('azure-dev.tasks.dotenv.taskFailed', "Child task '{0}' failed, exit code was {1}", target.name, exitCode), 'bold');
        context.telemetry.properties.exitCode = exitCode.toString();
        context.telemetry.properties.failedTaskDuration = duration.asSeconds().toString();
        context.telemetry.properties.failedTaskName = target.name;
        return DotEnvTaskResult.ErrorChildTaskFailed;
    } else {
        const durationStr = formatDuration(duration);
        writer.writeLine(localize('azure-dev.tasks.dotenv.taskSucceeded', "Child task '{0}' succeeded, took {1}", target.name, durationStr));
        return DotEnvTaskResult.Succeeded;
    }
}

async function getEnvVars(envFilePath: vscode.Uri): Promise<{ [key: string]: string }> {
    const contents = await vscode.workspace.fs.readFile(envFilePath);
    const result = dotenv.parse(contents.toString());
    return result;
}

function formatDuration(d: duration.Duration): string {
    let retval = d.format("H[h] m[m] s.SSS[s]");
    retval = retval.replace(/\b0h\b/,'').replace(/\b0m\b/,''); // Remove zero-hours and zero-minutes parts of the string
    retval = retval.replace(/^\s+/,''); // Remove whitespace from beginning of the string
    return retval;
}
