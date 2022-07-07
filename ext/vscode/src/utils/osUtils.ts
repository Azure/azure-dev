// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as os from 'os';

export function isWindows(): boolean {
    return os.platform() === 'win32';
}

export function isMac(): boolean {
    return os.platform() === 'darwin';
}

export function isArm64Mac(): boolean {
    return isMac() && os.arch() === 'arm64';
}

export function isLinux(): boolean {
    return os.platform() !== 'win32' && os.platform() !== 'darwin';
}
