// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { WorkspaceResource } from '@microsoft/vscode-azureresources-api';
import { IActionContext } from '@microsoft/vscode-azext-utils';
import { expect } from 'chai';
import { AzureDevCliApplication } from '../../../views/workspace/AzureDevCliApplication';
import { AzDevShowResults, AzureDevShowProvider } from '../../../services/AzureDevShowProvider';
import { AzureDevEnvListProvider } from '../../../services/AzureDevEnvListProvider';
import { AzureDevEnvValuesProvider } from '../../../services/AzureDevEnvValuesProvider';

suite('AzureDevCliApplication Tests', () => {
    // Captures the errorHandling context that AzureDevCliApplication passes into getShowResults so the
    // test can assert the passive tree-resolution path opts out of the modal "Report Issue" dialog.
    function buildApplication(showProvider: AzureDevShowProvider): AzureDevCliApplication {
        const resource = { id: '/test/azure.yaml', name: 'test-app' } as unknown as WorkspaceResource;
        const envListProvider = {} as unknown as AzureDevEnvListProvider;
        const envValuesProvider = {} as unknown as AzureDevEnvValuesProvider;

        return new AzureDevCliApplication(
            resource,
            () => { /* no-op refresh */ },
            showProvider,
            envListProvider,
            envValuesProvider,
            new Set<string>(),
            () => { /* no-op toggle */ },
            /* includeEnvironments */ false
        );
    }

    test('Suppresses the error dialog when show fails during passive tree resolution', async () => {
        let capturedSuppressDisplay: boolean | undefined;

        const failingShowProvider: AzureDevShowProvider = {
            getShowResults: (context: IActionContext): Promise<AzDevShowResults> => {
                // Record the flag at call time - AzureDevCliApplication must set it before invoking show.
                capturedSuppressDisplay = context.errorHandling.suppressDisplay;
                return Promise.reject(new Error('Process exited with code 1'));
            },
        };

        const application = buildApplication(failingShowProvider);

        // Resolving children triggers the show call. The failure is swallowed (suppressDisplay + no
        // rethrow), so this must not throw and must not surface a modal dialog.
        const children = await application.getChildren();

        expect(capturedSuppressDisplay, 'getShowResults must be invoked with suppressDisplay enabled').to.equal(true);
        // Even though show failed, the Services node is still returned (with no services).
        expect(children).to.have.lengthOf(1);
    });
});
