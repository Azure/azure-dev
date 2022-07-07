// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as path from 'path';
import { window, workspace, WorkspaceFolder } from 'vscode';

const variableMatcher: RegExp = /\$\{[a-z.\-_:]+\}/ig;
const configVariableMatcher: RegExp = /\$\{config:([a-z.\-_]+)\}/i;

type ResolvableSingle = string | number | { [key: string]: string } | undefined; 
type Resolvable = ResolvableSingle | ResolvableSingle[];

// We support only a limited set of most commonly-used variables with this implementation (including ability to resolve custom variables).
// Multi-folder workspace syntax is not supported either (https://code.visualstudio.com/docs/editor/variables-reference#_variables-scoped-per-workspace-folder)
// However, if customer demand exists, it should be easy to add support for more types of variables.
// Ultimately, VS Code extension API should add an API to resolve configuration variables,
// but until https://github.com/microsoft/vscode/issues/140056, or similar proposal, is implemented, we are stuck with our implementation.
export function resolveVariables<T extends Resolvable>(target: T, folder?: WorkspaceFolder, additionalVariables?: { [key: string]: string }): typeof target {
    if (!folder && workspace.workspaceFolders && workspace.workspaceFolders.length === 1) {
        folder = workspace.workspaceFolders[0];
    }

    if (!target) {
        return target;
    } else if (typeof (target) === 'string') {
        return target.replace(variableMatcher, (match: string) => {
            return resolveSingleVariable(match, folder, additionalVariables);
        }) as typeof target;
    } else if (typeof (target) === 'number') {
        return target;
    } else if (Array.isArray(target)) {
        return target.map(value => resolveVariables(value, folder, additionalVariables)) as typeof target;
    } else {
        // target is { [key: string]: string }
        const result: { [key: string]: string } = {};
        for (const key of Object.keys(target)) {
            result[key] = resolveVariables(target[key], folder, additionalVariables);
        }
        return result as typeof target;
    }
}

function resolveSingleVariable(variable: string, folder?: WorkspaceFolder, additionalVariables?: { [key: string]: string }): string {
    if (folder) {
        switch (variable) {
            case '${workspaceFolder}':
            case '${workspaceRoot}':
                return path.normalize(folder.uri.fsPath);
            
            case '${relativeFile}': {
                const activeFilePath = getActiveFilePath();
                if (activeFilePath) {
                    return path.relative(path.normalize(folder.uri.fsPath), activeFilePath);
                } else {
                    return '';
                }
            }
        }
    }

    // Replace additional variables
    const variableNameOnly = variable.replace(/[${}]/ig, '');
    const replacement = additionalVariables?.[variable] ?? additionalVariables?.[variableNameOnly];
    if (replacement !== undefined) {
        return replacement;
    }

    // Replace config variables
    const configMatch = configVariableMatcher.exec(variable);
    if (configMatch && configMatch.length > 1) {
        const configName: string = configMatch[1]; // Index 1 is the "something.something" group of "${config:something.something}"
        const config = workspace.getConfiguration();
        const configValue = config.get(configName);

        // If it's a simple value we'll return it
        if (typeof (configValue) === 'string') {
            return configValue;
        } else if (typeof (configValue) === 'number' || typeof (configValue) === 'boolean') {
            return configValue.toString();
        }
    }

    // Replace other variables
    switch (variable) {
        case '${file}':
            return getActiveFilePath();
        default:
    }

    return variable; // Return as-is, we don't know what to do with it
}

function getActiveFilePath(): string {
    const activeFilePath = window?.activeTextEditor?.document?.fileName || '';
    if (activeFilePath) {
        return path.normalize(activeFilePath);
    } else {
        return '';
    }
}

