// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import * as sinon from 'sinon';
import * as vscode from 'vscode';
import { AzureResourcesExtensionApi } from '@microsoft/vscode-azureresources-api';
import { RevealStep } from '../../../commands/azureWorkspace/wizard/RevealStep';
import { RevealResourceWizardContext } from '../../../commands/azureWorkspace/wizard/PickResourceStep';

suite('RevealStep', () => {
    let step: RevealStep;
    let sandbox: sinon.SinonSandbox;
    let revealStub: sinon.SinonStub;
    let executeCommandStub: sinon.SinonStub;

    setup(() => {
        sandbox = sinon.createSandbox();

        revealStub = sandbox.stub().resolves(undefined);
        const mockApi = {
            resources: {
                revealAzureResource: revealStub,
            } as unknown as AzureResourcesExtensionApi['resources']
        };
        const mockGetApi = sandbox.stub().resolves(mockApi as AzureResourcesExtensionApi);
        step = new RevealStep(mockGetApi);

        executeCommandStub = sandbox.stub(vscode.commands, 'executeCommand').resolves();
    });

    teardown(() => {
        sandbox.restore();
    });

    suite('shouldExecute', () => {
        test('returns true when azureResourceId is present', () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app'
            };

            const result = step.shouldExecute(context as RevealResourceWizardContext);

            expect(result).to.equal(true);
        });

        test('returns false when azureResourceId is missing', () => {
            const context: Partial<RevealResourceWizardContext> = {};

            const result = step.shouldExecute(context as RevealResourceWizardContext);

            expect(result).to.equal(false);
        });

        test('returns false when azureResourceId is empty string', () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: ''
            };

            const result = step.shouldExecute(context as RevealResourceWizardContext);

            expect(result).to.equal(false);
        });
    });

    suite('execute', () => {
        test('focuses Azure Resources view', async () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app'
            };

            await step.execute(context as RevealResourceWizardContext);

            expect(executeCommandStub.calledWith('azureResourceGroups.focus')).to.be.true;
        });

        test('calls revealAzureResource with correct resource ID and options', async () => {
            const azureResourceId = '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app';
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId
            };
            
            await step.execute(context as RevealResourceWizardContext);

            expect(revealStub.calledWith(azureResourceId, { select: true, focus: true, expand: true })).to.be.true;
        });
    });

    suite('priority', () => {
        test('has correct priority value', () => {
            expect(step.priority).to.equal(100);
        });
    });
});
