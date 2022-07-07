// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { IExperimentationServiceAdapter, UIExtensionVariables } from '@microsoft/vscode-azext-utils';
import { ActivityStatisticsService } from './telemetry/activityStatisticsService';

export interface AzDevExtensionVariables extends UIExtensionVariables {
    azureDevExtensionId: 'ms-azuretools.azure-dev';
    azureDevExtensionNamespace: 'vscode:/extensions/ms-azuretools.azure-dev';
    userAgent: string;
    experimentationSvc: IExperimentationServiceAdapter | undefined;
    activitySvc: ActivityStatisticsService
}

const ext = {
    // The literal-type, single-value "const" properties need to be set on the singleton object nevertheless.
    azureDevExtensionId: 'ms-azuretools.azure-dev',
    azureDevExtensionNamespace: 'vscode:/extensions/ms-azuretools.azure-dev'
} as AzDevExtensionVariables;
export default ext;
