import { ExecOptions, SpawnOptionsWithoutStdio } from "child_process";
import { executeCommand, spawnCommand } from "../common/util";

export interface GitCommitOptions {
    all?: boolean
    empty?: boolean
}

export class GitRepo {
    private execOptions: ExecOptions;
    private spawnOptions: SpawnOptionsWithoutStdio;

    constructor(private path: string) {
        this.execOptions = { cwd: this.path };
        this.spawnOptions = { cwd: this.path };
    }

    public isValidRepo = async () => {
        try {
            const result = await executeCommand("git rev-parse --is-inside-work-tree 2>/dev/null", this.execOptions);
            return result === "true";
        }
        catch (err) {
            return false;
        }
    }

    public init = async () => {
        await executeCommand("git init", this.execOptions);
    }

    public clone = async (name: string, url: string) => {
        await executeCommand(`git clone -o ${name} ${url} .`, this.execOptions);
    }

    public fetch = async (remote: string) => {
        await executeCommand(`git fetch ${remote}`, this.execOptions);
    }

    public getCurrentBranch = async () => {
        return await executeCommand("git branch --show-current", this.execOptions);
    }

    public checkoutBranch = async (branchName: string): Promise<void> => {
        await executeCommand(`git checkout ${branchName}`, this.execOptions);
    }

    public pull = async (remote: string, branchName?: string): Promise<void> => {
        if (branchName) {
            await executeCommand(`git pull ${remote} ${branchName}`, this.execOptions);
        } else {
            await executeCommand(`git pull ${remote}`, this.execOptions);
        }
    }

    public branchExists = async (branchName: string): Promise<boolean> => {
        try {
            const results = await executeCommand(`git branch -a --contains ${branchName}`, this.execOptions);
            return results.indexOf(branchName) > -1;
        }
        catch (err) {
            return false;
        }
    }

    public isEmptyRepo = async (remote: string): Promise<boolean> => {
        const remoteBranches = await this.getRemoteBranches(remote);
        return remoteBranches.length === 0;
    }

    public getRemoteBranches = async (remote: string): Promise<string[]> => {
        return await spawnCommand("git", ["ls-remote", remote], this.spawnOptions);
    }

    public remoteBranchExists = async (remote: string, branchName: string): Promise<boolean> => {
        const results = await executeCommand(`git ls-remote ${remote} refs/heads/${branchName}`, this.execOptions);
        return results.indexOf(branchName) > -1;
    }

    public createBranch = async (branchName: string): Promise<void> => {
        await executeCommand(`git checkout -b ${branchName}`, this.execOptions);
    }

    public status = async (): Promise<string[]> => {
        return await spawnCommand("git", ["status", "-s"], this.spawnOptions);
    }

    public diffFiles = async (base: string): Promise<string[]> => {
        return await spawnCommand("git", ["diff", "--name-only", base], this.spawnOptions)
    }

    public hasChanges = async (): Promise<boolean> => {
        const status = await this.status();
        return status.length > 0;
    }

    public hasChangesFromBase = async (base: string): Promise<boolean> => {
        const diff = await this.diffFiles(base)
        return diff.length > 0
    }

    public addAll = async (): Promise<void> => {
        await executeCommand("git add .", this.execOptions);
    }

    public commit = async (message: string, options?: GitCommitOptions): Promise<void> => {
        const settings: GitCommitOptions = {
            all: false,
            empty: false,
            ...options
        };

        const command = ["git", "commit", "-m", `"${message}"`];
        if (settings.empty) {
            command.push("--allow-empty")
        }

        if (settings.all) {
            await this.addAll();
        }

        await executeCommand(command.join(" "), this.execOptions);
    }

    public push = async (remote: string, branchName: string, setUpstream: boolean = true): Promise<void> => {
        const command = ["git", "push", remote, branchName];
        if (setUpstream) {
            command.push("-u")
        }

        await executeCommand(command.join(" "), this.execOptions);
    }

    public remoteExists = async (name: string): Promise<boolean> => {
        try {
            await this.getRemote(name);
            return true;
        } catch {
            return false;
        }
    }

    public getRemote = async (name: string): Promise<string> => {
        return await executeCommand(`git remote get-url ${name}`, this.execOptions);
    }

    public addRemote = async (name: string, url: string): Promise<void> => {
        await executeCommand(`git remote add ${name} ${url}`, this.execOptions);
    }
}