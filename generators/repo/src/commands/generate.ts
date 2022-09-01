import yaml from "yamljs";
import { IOptions } from "glob";
import path from "path";
import os from "os";
import fs from "fs/promises";
import ansiEscapes from "ansi-escapes";
import chalk from "chalk";
import { cleanDirectoryPath, ensureRelativeBasePath, copyFile, createRepoUrlFromRemote, ensureDirectoryPath, getGlobFiles, getRepoPropsFromRemote, isStringNullOrEmpty, RepoProps, writeHeader,isFilePath } from "../common/util";
import { AssetRule, RewriteRule, GitRemote, RepomanCommand, RepomanCommandOptions, RepoManifest } from "../models";
import { GitRepo } from "../tools/git";

export interface GenerateCommandOptions extends RepomanCommandOptions {
    source: string
    output: string
    templateFile: string
    update: boolean
    https?: boolean
    failOnUpdateError?: boolean
    remote?: string
    branch?: string
    message?: string
    resultsFile?: string
}

export interface RemotePushResult extends RepoProps {
    pushed: boolean
    hasChanges: boolean
    hasChangesFromBase: boolean
    remote: string
    branch: string
    branchUrl?: string
    compareUrl?: string
}

export class GenerateCommand implements RepomanCommand {
    private sourcePath: string;
    private templateFile: string;
    private manifest: RepoManifest;
    private outputPath: string;
    private generatePath: string;
    private assetRules: AssetRule[];
    private rewriteRules: RewriteRule[];

    constructor(private options: GenerateCommandOptions) {
        this.sourcePath = path.resolve(path.normalize(options.source));
        const rootOutputPath = path.resolve(path.normalize(options.output));
        this.templateFile = path.join(this.sourcePath, options.templateFile);

        try {
            this.manifest = yaml.load(this.templateFile);
            this.outputPath = path.join(rootOutputPath, this.manifest.metadata.name);
            this.generatePath = path.join(rootOutputPath, "generated");

            this.assetRules = [...this.manifest.repo.assets];

            if (this.manifest.repo.includeProjectAssets) {
                this.assetRules.unshift({
                    from: ".",
                    to: ".",
                    ignore: ["repo.y[a]ml"]
                });
            }

            this.rewriteRules =(this.manifest.repo.rewrite) ? [...this.manifest.repo.rewrite?.rules] : [];
        }
        catch (err) {
            console.error(chalk.red(`Repo template manifest not found at '${this.templateFile}'`));
            throw err;
        }
    }

    public execute = async () => {
        writeHeader(`Project: ${this.manifest.metadata.name}`, { color: chalk.cyanBright, char: "=" });

        console.info(chalk.white(`Template: ${chalk.green(this.templateFile)}`));
        console.info(chalk.white(`Source: ${chalk.green(this.sourcePath)}`));
        console.info(chalk.white(`Destination: ${chalk.green(this.outputPath)}`));
        console.info();

        await ensureDirectoryPath(this.generatePath);
        await cleanDirectoryPath(this.generatePath);
        console.info(chalk.cyan('Repo generation started...'));

        for (const rule of this.assetRules) {
            await this.processAssetRule(rule);
        }

        console.info();

        for (const rule of this.rewriteRules) {
            await this.processRewriteRule(rule);
        }
        console.info(chalk.cyan('Repo generation completed.'));
        console.info();

        const repo = await this.validateRepo();
        if (this.options.update && repo) {
            await this.updateRemotes(repo);
        }
    }

    private validateRepo = async (): Promise<GitRepo | undefined> => {
        const repo = new GitRepo(this.outputPath);

        if (!this.manifest.repo.remotes || this.manifest.repo.remotes.length === 0) {
            console.warn(chalk.yellowBright("Remotes manifest is missing 'remotes' configuration and is unable to push changes"));
            return;
        }

        return repo;
    }

    private updateRemotes = async (repo: GitRepo): Promise<void> => {
        const results: RemotePushResult[] = [];

        for (const remote of this.manifest.repo.remotes) {
            try {
                const remotePushResult = await this.updateRemote(repo, remote);
                results.push(remotePushResult);
            }
            catch (err) {
                console.error(chalk.red(`Error updating remote '${remote.name}', Message: ${err}`))
                if (this.options.failOnUpdateError) {
                    throw err;
                }
            }

            console.info();
        }

        await this.writeResultsFile(results);
    }

    private updateRemote = async (repo: GitRepo, remote: GitRemote): Promise<RemotePushResult> => {
        const defaultBranch = remote.branch || "main";
        const targetBranch = this.options.branch || defaultBranch;
        const repoProps = getRepoPropsFromRemote(remote.url);

        let targetRemote = remote;
        const remoteHttpUrl = createRepoUrlFromRemote(targetRemote.url);

        const branchUrl = `${remoteHttpUrl}/tree/${targetBranch}`;
        const compareUrl = `${remoteHttpUrl}/compare/${defaultBranch}...${targetBranch}`

        let updateResult: RemotePushResult = {
            hasChanges: false,
            hasChangesFromBase: false,
            pushed: false,
            remote: remote.name,
            branch: targetBranch,
            branchUrl,
            compareUrl,
            ...repoProps
        };

        if (this.options.https && repoProps.host == 'github.com') {
            console.info(chalk.white(`Using HTTPS URL for GitHub repo`))
            targetRemote = {
                name: remote.name,
                url: `${remoteHttpUrl}.git`,
                branch: remote.branch,
            } as GitRemote;
        }

        writeHeader(`Remote: ${targetRemote.name || "???"}`, { color: chalk.cyanBright });
        console.info(chalk.white(`Remote: ${chalk.cyan(targetRemote.url)}`));
        console.info(chalk.white(`Host: ${chalk.cyan(updateResult.host)}`));
        console.info(chalk.white(`Org: ${chalk.cyan(updateResult.org)}`));
        console.info(chalk.white(`Repo: ${chalk.cyan(updateResult.repo)}`));
        console.info();

        if (isStringNullOrEmpty(targetRemote.name)) {
            console.error(chalk.red(`Missing remote name in repo template: ${chalk.magentaBright(this.templateFile)}`));
            return updateResult;
        }

        if (isStringNullOrEmpty(targetRemote.url)) {
            console.error(chalk.red(`Remote url is required in repo template: ${chalk.magentaBright(this.templateFile)}`));
            return updateResult;
        }

        if (this.options.remote && this.options.remote !== targetRemote.name) {
            console.warn(chalk.yellowBright(`Skipping remote ${targetRemote.name} (${targetRemote.url})`));
            return updateResult;
        }

        await this.initRemote(repo, targetRemote, defaultBranch, targetBranch);
        const hasChanges = await this.commitChanges(repo, defaultBranch, targetBranch);
        const hasChangesFromBase = await repo.hasChangesFromBase(defaultBranch)

        if (!hasChanges) {
            return { ...updateResult, hasChanges, hasChangesFromBase };
        }

        console.info(chalk.cyan(`Pushing changes to ${chalk.cyanBright(targetBranch)}...`));
        await repo.push(targetRemote.name, targetBranch);

        updateResult = {
            ...updateResult,
            hasChanges,
            hasChangesFromBase,
            pushed: true
        };

        console.info();
        console.info(chalk.white(`Changes are available @ ${chalk.greenBright(branchUrl)}`));
        console.info(chalk.white(`Compare @ ${chalk.greenBright(compareUrl)}`));

        return updateResult;
    }

    private initRemote = async (repo: GitRepo, remote: GitRemote, defaultBranch: string, targetBranch: string) => {
        await ensureDirectoryPath(this.outputPath);
        await cleanDirectoryPath(this.outputPath);

        console.info(chalk.white(`Cloning repo for remote...`));
        await repo.clone(remote.name, remote.url);

        const defaultBranchExists = await repo.remoteBranchExists(remote.name, defaultBranch);

        if (!defaultBranchExists) {
            console.warn(chalk.yellowBright(`Remote does not have branch ${chalk.cyan(defaultBranch)}`));
            console.info(`Creating default branch ${chalk.cyan(defaultBranch)}...`);
            await repo.createBranch(defaultBranch);
            await repo.commit("Initial Commit", { empty: true });
            await repo.push(remote.name, defaultBranch);
        } else {
            await repo.checkoutBranch(defaultBranch);
            const pullBranchExists = await repo.remoteBranchExists(remote.name, defaultBranch);
            if (pullBranchExists) {
                console.info(chalk.cyan(`Pulling changes from branch ${chalk.cyanBright(defaultBranch)}...`));
                await repo.pull(remote.name, defaultBranch);
            }
        }

        const branchExists = await repo.remoteBranchExists(remote.name, targetBranch);
        if (!branchExists) {
            console.warn(chalk.yellowBright(`Branch '${targetBranch}' doesn't exist on remote`))
            console.info(chalk.white(`Creating new branch: ${chalk.cyan(targetBranch)}`));
            await repo.createBranch(targetBranch);
        }

        const currentBranch = await repo.getCurrentBranch();
        if (currentBranch !== targetBranch) {
            console.info(chalk.white(`Checking out branch: ${chalk.cyan(targetBranch)}`));
            await repo.checkoutBranch(targetBranch);
        }
    }

    private stageFiles = async (repo: GitRepo) => {
        console.info(chalk.white(`Staging files from generated output`));
        const globOptions: IOptions = {
            cwd: this.generatePath,
            nodir: true,
            dot: true,
            matchBase: true,
        }
        const files = await getGlobFiles("**/*", globOptions);

        // Clean the folder except for the .git folder then overlay changes
        // This correctly detects for file deletes/renames/moves
        await cleanDirectoryPath(this.outputPath, false);
        await this.copyFiles(files, this.generatePath, this.outputPath);
        await repo.addAll();
    }

    private commitChanges = async (repo: GitRepo, defaultBranch: string, targetBranch: string): Promise<boolean> => {
        await this.stageFiles(repo);

        const hasChanges = await repo.hasChanges();
        if (!hasChanges) {
            console.warn(chalk.yellowBright(`No new changes found to commit when comparing between ${chalk.cyan(defaultBranch)} and ${chalk.cyan(targetBranch)}.`));
            return false;
        }

        const changes = await repo.status();
        console.info();
        console.info(chalk.white(`Found the following ${chalk.cyan(changes.length)} change(s)...`));
        changes.map(line => console.info(chalk.white(`- ${line}`)));

        const commitMessage = this.options.message || "Synchronize repo from Repoman"
        console.info(chalk.white(`Committing ${chalk.cyan(changes.length)} change(s) to remote with message: ${chalk.green(commitMessage)}`));
        console.info();

        await repo.commit(commitMessage);
        return true;
    }

    private writeResultsFile = async (results: RemotePushResult[]) => {
        return new Promise<void>(async (resolve, reject) => {
            const pushedResults = results.filter(r => r.hasChangesFromBase);

            if (pushedResults.length === 0 || !this.options.resultsFile) {
                return resolve();
            }

            const resultsFilePath = path.resolve(path.normalize(this.options.resultsFile));
            const resultsFile = await fs.open(resultsFilePath, "a+");
            const resultsStream = resultsFile.createWriteStream();

            try {

                const output: string[] = [];
                output.push(`### Project: **${this.manifest.metadata.name}**`);

                for (const result of pushedResults) {
                    output.push(`#### Remote: **${result.remote}**`);
                    output.push(`##### Branch: **${result.branch}**`);
                    output.push('');
                    output.push('You can initialize this project with:');
                    output.push('```bash');
                    output.push(`azd init -t ${result.org}/${result.repo} -b ${result.branch}`);
                    output.push('```');
                    output.push('');
                    output.push(`[View Changes](${result.branchUrl}) | [Compare Changes](${result.compareUrl})`);
                    output.push('');
                    output.push('---');
                    output.push('');
                }

                if (this.options.debug) {
                    console.debug(chalk.grey("RESULTS OUTPUT"))
                    console.debug(chalk.grey(output.join(os.EOL)));
                }

                resultsStream.write(output.join(os.EOL), (error) => {
                    if (error) {
                        return reject(error);
                    }

                    resolve();
                })
            }
            finally {
                resultsStream.close();
                resultsFile.close();
                console.info(chalk.cyan(`Push results written to '${resultsFilePath}'`));
            }
        });
    }

    private processAssetRule = async (rule: AssetRule) => {
        const absoluteSourcePath = path.resolve(this.sourcePath, rule.from);
        const absoluteDestPath = path.join(this.generatePath, rule.to);
        console.info(chalk.white(`Copying asset(s) from ${chalk.cyan(rule.from)} to ${chalk.cyan(rule.to)}...`));
        // check if this is filepath
        if (await isFilePath(absoluteSourcePath)) {
            await copyFile(absoluteSourcePath, absoluteDestPath);
            return;
        }
        
        // Default to all files if no patterns defined
        const patterns = rule.patterns ?? ["**/*"];
        const globOptions: IOptions = {
            cwd: absoluteSourcePath,
            ignore: rule.ignore,
            nodir: true,
            dot: true,
            matchBase: true,
        };

        for (const pattern of patterns) {
            const files = await getGlobFiles(pattern, globOptions);
            if (files.length === 0) {
                console.warn(chalk.yellowBright(`- No files found matching pattern '${pattern}' in '${globOptions.cwd}'`))
                continue;
            }
            console.info(chalk.white(ansiEscapes.cursorPrevLine + `Copying assets from ${chalk.cyan(rule.from)} to ${chalk.cyan(rule.to)}... (${files.length} files)`));
            await this.copyFiles(files, absoluteSourcePath, absoluteDestPath);
        }
    }

    private copyFiles = async (files: string[], sourceDirectoryPath: string, destDirectoryPath: string) => {
        for (const filePath of files) {
            const sourcePath = path.join(sourceDirectoryPath, filePath);
            const destPath = path.join(destDirectoryPath, filePath);
            await copyFile(sourcePath, destPath);
        }
    }

    private processRewriteRule = async(rule: RewriteRule) => {
        const globOptions: IOptions = { 
            cwd: this.generatePath, 
            ignore: rule.ignore,
            matchBase: true,
            nodir: true
        };
        const patterns = rule.patterns ?? [];
        if(patterns.length == 0){
            console.warn(chalk.yellowBright(`Skipping Rewrite Rule ${rule.from} => ${rule.to}. No pattern found. Add a pattern of '**/*' to apply this rule to all files.`));
        }
        for (const pattern of patterns) {
            const files = await getGlobFiles(pattern, globOptions);
            for (const filePath of files) {
                await this.rewritePath(rule, filePath);
            }
        }
    }

    private rewritePath = async(rule: RewriteRule, filePath: string) => {
        const destFilePath = path.join(this.generatePath, filePath);
        const destFolderPath = path.dirname(destFilePath);
        const buffer = await fs.readFile(destFilePath);
        let contents = buffer.toString('utf8');
        if(contents.indexOf(rule.from) == -1) return;

        console.info(chalk.cyan(` -> Rewriting relative paths ${rule.from} => ${rule.to} for file "${filePath}"`));
        contents = contents.replaceAll(rule.from, rule.to);

        // Normalize transformed paths
        const pathRegex = new RegExp(/((?:\.{1,2}[\/\\]{1,2})+[^'"\s]*)/gm);
        const matches = contents.match(pathRegex);
        if (matches && matches.length > 0) {
            for (const match of matches) {
                if(match.indexOf(rule.to) > -1){
                    // Generate the absolute path to the referenced match
                    let refPath = path.resolve(destFolderPath, path.normalize(match))
                    // Generate the relative path between the current processed file dir path & the referenced match path
                    let relativePath = path.relative(destFolderPath, refPath)
                    relativePath = ensureRelativeBasePath(relativePath);
                    // Finally convert the path back to a POSIX compatible path
                    relativePath = relativePath.split(path.sep).join(path.posix.sep)

                    contents = contents.replaceAll(match, relativePath);

                    if (this.options.debug) {
                        console.log(chalk.grey(` -> Rewriting relative path ${match} => ${relativePath} in ${destFilePath}`));
                    }
                }
            }
        }
        await fs.writeFile(destFilePath, contents);
        
    }
}