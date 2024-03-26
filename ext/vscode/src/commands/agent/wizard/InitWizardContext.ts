// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IActionContext } from '@microsoft/vscode-azext-utils';

export interface InitWizardContext extends IActionContext {
    workspaceFolder?: vscode.WorkspaceFolder;

    templateUrl?: string;
    fromSource?: boolean;

    environmentName?: string;
}
