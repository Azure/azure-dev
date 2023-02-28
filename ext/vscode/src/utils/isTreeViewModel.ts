// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { isWrapper, Wrapper } from '@microsoft/vscode-azureresources-api';
import * as vscode from 'vscode';

export type TreeViewModel = Wrapper;

export function isTreeViewModel(selectedItem: vscode.Uri | TreeViewModel | undefined | unknown): selectedItem is TreeViewModel {
    return isWrapper(selectedItem);
}
