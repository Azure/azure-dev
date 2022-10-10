// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

export interface TreeViewModel {
    unwrap<T>(): T;
}

export function isTreeViewModel(selectedItem: vscode.Uri | TreeViewModel | undefined): selectedItem is TreeViewModel {
    return !!(selectedItem as TreeViewModel).unwrap;
}
