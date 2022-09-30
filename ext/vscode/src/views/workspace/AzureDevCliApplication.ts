import * as vscode from 'vscode';
import { AzureDevCliModel } from "./AzureDevCliModel";

export class AzureDevCliApplication implements AzureDevCliModel {
    getChildren(): Promise<AzureDevCliModel[]> {
        throw new Error("Method not implemented.");
    }

    getTreeItem(): Promise<vscode.TreeItem> {
        throw new Error("Method not implemented.");
    }
}