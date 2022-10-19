/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import type { Environment } from '@azure/ms-rest-azure-env';
import { AzExtResourceType } from '@microsoft/vscode-azext-utils';
import * as vscode from 'vscode';

/**
 * Represents the base type for all application and workspace resources.
 */
export interface ResourceBase {
    /**
     * The ID of this resource.
     *
     * @remarks This value should be unique across all resources.
     */
    readonly id: string;

    /**
     * The display name of this resource.
     */
    readonly name: string;
}

/**
 * Represents the base type for models of resources and their child items.
 */
export interface ResourceModelBase {
    /**
     * The ID of this model.
     *
     * @remarks This value should be unique across all models of its type.
     */
    readonly id?: string;
}

/**
 * The base interface for providers of application and workspace resources.
 */
export interface ResourceProvider<TResourceSource, TResource extends ResourceBase> {
    /**
     * Fired when the provider's resources have changed.
     */
    readonly onDidChangeResource?: vscode.Event<TResource | undefined>;

    /**
     * Called to supply the resources used as the basis for the resource views.
     *
     * @param source The source from which resources should be generated.
     *
     * @returns The resources to be displayed in the resource view.
     */
    getResources(source: TResourceSource): vscode.ProviderResult<TResource[]>;
}

/**
 * The base interface for visualizers of application and workspace resources.
 */
export interface BranchDataProvider<TResource extends ResourceBase, TModel extends ResourceModelBase> extends vscode.TreeDataProvider<TModel> {
    /**
     * Get the children of `element`.
     *
     * @param element The element from which the provider gets children. Unlike a traditional tree data provider, this will never be `undefined`.
     *
     * @return Children of `element`.
     */
    getChildren(element: TModel): vscode.ProviderResult<TModel[]>;

    /**
     * A branch data provider need not (and should not) implement this function.
     *
     * @remarks While VS Code would normally call this function on a tree data provider, it is not used by the Azure Resource extension as part of a branch data provider.
     */
    getParent?: never;

    /**
     * Called to get the provider's model element for a specific resource.
     *
     * @remarks getChildren() assumes that the provider passes a known (TModel) model item, or undefined when getting the "root" children.
     *          However, branch data providers have no "root" so this function is called for each matching resource to obtain a starting branch item.
     *
     * @returns The provider's model element for `resource`.
     */
    getResourceItem(element: TResource): TModel | Thenable<TModel>;
}

/**
 * Represents a means of obtaining authentication data for an application subscription.
 */
export interface ApplicationAuthentication {
    /**
     * Gets a VS Code authentication session for an application subscription.
     *
     * @param scopes The scopes for which the authentication is needed.
     *
     * @returns A VS Code authentication session or undefined, if none could be obtained.
     */
    getSession(scopes?: string[]): vscode.ProviderResult<vscode.AuthenticationSession>;
}

/**
 * Represents an Azure subscription.
 */
export interface ApplicationSubscription {
    /**
     * Access to the authentication session associated with this subscription.
     */
    readonly authentication: ApplicationAuthentication;

    /**
     * The Azure environment to which this subscription belongs.
     */
    readonly environment: Environment;

    /**
     * Whether this subscription belongs to a custom cloud.
     */
    readonly isCustomCloud: boolean;

    /**
     * The display name of this subscription.
     */
    readonly name: string;

    /**
     * The ID of this subscription.
     */
    readonly subscriptionId: string;

    /**
     * The tenant to which this subscription belongs or undefined, if not associated with a specific tenant.
     */
    readonly tenantId?: string;
}

/**
 * Represents a type of resource as designated by Azure.
 */
export interface AzureResourceType {
    /**
     * The kinds of resources that this type can represent.
     */
    readonly kinds?: string[];

    /**
     * The (general) type of resource.
     */
     readonly type: string;
}

/**
 * Represents an individual resource in Azure.
 */
export interface ApplicationResource extends ResourceBase {
    /**
     * The Azure-designated type of this resource.
     */
    readonly azureResourceType: AzureResourceType;

    /**
     * The location in which this resource exists.
     */
    readonly location?: string;

    /**
     * The resource group to which this resource belongs.
     */
    readonly resourceGroup?: string;

    /**
     * The type of this resource.
     *
     * @remarks This value is used to map resources to their associated branch data provider.
     */
    readonly resourceType?: AzExtResourceType;

    /**
     * The Azure subscription to which this resource belongs.
     */
    readonly subscription: ApplicationSubscription;

    /**
     * The tags associated with this resource.
     */
    readonly tags?: {
        [propertyName: string]: string;
    };
}

/**
 * Represents a model of an individual application resource or its child items.
 */
export interface ApplicationResourceModel extends ResourceModelBase {
    /**
     * The Azure ID of this resource.
     *
     * @remarks This property is expected to be implemented on "application-level" resources, but may also be applicable to its child items.
     */
    readonly azureResourceId?: string;

    /**
     * The URL of the area of Azure portal related to this item.
     */
    readonly portalUrl?: vscode.Uri;
}

/**
 * A provider for supplying items for the application resource tree (e.g. Cosmos DB, Storage, etc.).
 */
export type ApplicationResourceProvider = ResourceProvider<ApplicationSubscription, ApplicationResource>;

/**
 * A provider for visualizing items in the application resource tree (e.g. Cosmos DB, Storage, etc.).
 */
export type ApplicationResourceBranchDataProvider<TModel extends ApplicationResourceModel> = BranchDataProvider<ApplicationResource, TModel>;

/**
 * Respresents a specific type of workspace resource.
 *
 * @remarks This value should be unique across all types of workspace resources.
 */
type WorkspaceResourceType = string;

/**
 * An indivdual root resource for a workspace.
 */
export interface WorkspaceResource extends ResourceBase {
    /**
     * The folder to which this resource belongs.
     */
    readonly folder: vscode.WorkspaceFolder;

    /**
     * The type of this resource.
     *
     * @remarks This value is used to map resources to their associated branch data provider.
     */
    readonly resourceType: WorkspaceResourceType;
}

/**
 * Represents a model of an individual workspace resource or its child items.
 */
export type WorkspaceResourceModel = ResourceModelBase;

/**
 * A provider for supplying items for the workspace resource tree (e.g., storage emulator, function apps in workspace, etc.).
 */
export type WorkspaceResourceProvider = ResourceProvider<vscode.WorkspaceFolder, WorkspaceResource>;

/**
 * A provider for visualizing items in the workspace resource tree (e.g., storage emulator, function apps in workspace, etc.).
 */
export type WorkspaceResourceBranchDataProvider<TModel extends WorkspaceResourceModel> = BranchDataProvider<WorkspaceResource, TModel>;

/**
 * The current (v2) Azure Resources extension API.
 */
export interface V2AzureResourcesApi extends AzureResourcesApiBase {
    /**
     * Registers a provider of application resources.
     *
     * @param provider The resource provider.
     *
     * @returns A disposable that unregisters the provider when disposed.
     */
    registerApplicationResourceProvider(provider: ApplicationResourceProvider): vscode.Disposable;

    /**
     * Registers an application resource branch data provider.
     *
     * @param type The Azure application resource type associated with the provider. Must be unique.
     * @param resolver The branch data provider for the resource type.
     *
     * @returns A disposable that unregisters the provider.
     */
    registerApplicationResourceBranchDataProvider<TModel extends ApplicationResourceModel>(type: AzExtResourceType, provider: ApplicationResourceBranchDataProvider<TModel>): vscode.Disposable;

    /**
     * Registers a provider of workspace resources.
     *
     * @param provider The resource provider.
     *
     * @returns A disposable that unregisters the provider.
     */
    registerWorkspaceResourceProvider(provider: WorkspaceResourceProvider): vscode.Disposable;

    /**
     * Registers a workspace resource branch data provider.
     *
     * @param type The workspace resource type associated with the provider. Must be unique.
     * @param provider The branch data provider for the resource type.
     *
     * @returns A disposable that unregisters the provider.
     */
    registerWorkspaceResourceBranchDataProvider<TModel extends WorkspaceResourceModel>(type: WorkspaceResourceType, provider: WorkspaceResourceBranchDataProvider<TModel>): vscode.Disposable;
}

/**
 * The base API for all versions of Azure Resources extension APIs.
 */
export interface AzureResourcesApiBase {
    /**
     * The version of this Azure Resources extension API.
     */
    readonly apiVersion: string;
}

/**
 * Options affecting the return of an Azure Resources extension API.
 */
export interface GetApiOptions {
    /**
     * The ID of the extension requesting the API.
     *
     * @remarks This is used for telemetry purposes, to measure which extensions are using the API.
     */
    readonly extensionId?: string;
}

/**
 * Exported object of the Azure Resources extension.
 */
export interface AzureResourcesApiManager {
    /**
     * Gets a specific version of the Azure Resources extension API.
     *
     * @typeparam TApi The type of the API.
     * @param versionRange The version of the API to return, specified as a potential (semver) range of versions.
     *
     * @returns The requested API or undefined, if no version of the API matches the specified range.
     */
    getApi<TApi extends AzureResourcesApiBase>(versionRange: string, options?: GetApiOptions): TApi | undefined
}
