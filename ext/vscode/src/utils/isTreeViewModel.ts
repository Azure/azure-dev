// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { isWrapper, Wrapper } from '@microsoft/vscode-azureresources-api';
import * as vscode from 'vscode';
import { AzureDevCliModel } from '../views/workspace/AzureDevCliModel';

export type TreeViewModel = Wrapper;

// eslint-disable-next-line @typescript-eslint/no-redundant-type-constituents
export function isTreeViewModel(selectedItem: vscode.Uri | TreeViewModel | undefined | unknown): selectedItem is TreeViewModel {
    return isWrapper(selectedItem);
}

export function isAzureDevCliModel(item: unknown): item is AzureDevCliModel {
    return !!item && typeof item === 'object' && 'context' in item && !!(item as AzureDevCliModel).context && 'configurationFile' in (item as AzureDevCliModel).context;
}
