// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { IExperimentationServiceAdapter, UIExtensionVariables } from '@microsoft/vscode-azext-utils';
import { ActivityStatisticsService } from './telemetry/activityStatisticsService';
import { Lazy } from './utils/lazy';

export interface AzDevExtensionVariables extends UIExtensionVariables {
    azureDevExtensionId: 'ms-azuretools.azure-dev';
    azureDevExtensionNamespace: 'vscode:/extensions/ms-azuretools.azure-dev';
    userAgent: string;
    experimentationSvc: IExperimentationServiceAdapter | undefined;
    activitySvc: ActivityStatisticsService
    extensionVersion: Lazy<string>
}

const ext = {
    // The literal-type, single-value "const" properties need to be set on the singleton object nevertheless.
    azureDevExtensionId: 'ms-azuretools.azure-dev',
    azureDevExtensionNamespace: 'vscode:/extensions/ms-azuretools.azure-dev',
    extensionVersion: new Lazy<string>(() => {
        const extension = vscode.extensions.getExtension('ms-azuretools.azure-dev');
        return extension?.packageJSON?.version ?? '';
    })
} as AzDevExtensionVariables;
export default ext;
