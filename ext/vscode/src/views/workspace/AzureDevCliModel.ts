// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { ResourceModelBase } from "./ResourceGroupsApi";

export interface AzureDevCliModelContext {
    readonly configurationFile: vscode.Uri;
}

export interface AzureDevCliModel extends ResourceModelBase {
    readonly context: AzureDevCliModelContext;

    getChildren(): AzureDevCliModel[] | Thenable<AzureDevCliModel[]>;
    getTreeItem(): vscode.TreeItem | Thenable<vscode.TreeItem>;
}

export type RefreshHandler = (model: AzureDevCliModel) => void;
