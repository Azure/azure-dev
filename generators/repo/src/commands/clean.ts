import yaml from "yamljs";
import path from "path";
import chalk from "chalk";
import { cleanDirectoryPath, createRepoUrlFromRemote, ensureDirectoryPath, getRepoPropsFromRemote, writeHeader } from "../common/util";
import { GitRemote, RepomanCommand, RepomanCommandOptions, RepoManifest } from "../models";
import { GitRepo } from "../tools/git";

export interface CleanCommandOptions extends RepomanCommandOptions {
    templateFile: string
    branch: string
    source: string
    output: string
    https?: boolean
    failOnCleanError?: boolean
}

export class CleanCommand implements RepomanCommand {
    private templateFile: string;
    private manifest: RepoManifest;
    private sourcePath: string;
    private outputPath: string;
    constructor(private options: CleanCommandOptions) {
        this.sourcePath = path.resolve(path.normalize(options.source))
        this.templateFile = path.join(this.sourcePath, options.templateFile);

        try {
            this.manifest = yaml.load(this.templateFile);
            this.outputPath = path.resolve(path.normalize(options.output))
        }
        catch (err) {
            console.error(chalk.red(`Repo template manifest not found at '${this.templateFile}'`));
            throw err;
        }
    }

    public execute = async () => {
        writeHeader(`Clean Command`);
    
        if(!this.validRemotes())
          return;

        console.info(chalk.white(`Repo clean started...`));

        this.manifest.repo.remotes.forEach(async remote => {
            try {
                let targetRemote = this.configureRemote(remote);
                await this.deleteRemoteBranch(targetRemote);
                console.info(chalk.cyan(`Branch ${targetRemote.branch} has been deleted from remote ${targetRemote.url}.`));
            }
            catch (err){
                console.error(chalk.red(err));
                if (this.options.failOnCleanError) {
                    throw err;
                }
            }
        });
        
        console.info(chalk.white(`Repo clean completed.`));
        console.info();
    }
    
    private deleteRemoteBranch = async (remote: GitRemote) => {
        if(!remote.branch){
            throw "Error Remote Branch is not specified";
        }

        const targetBranch: string = remote.branch?.toString();
        const repoName: string = this.manifest.metadata.name;

        await ensureDirectoryPath(this.outputPath);
        await cleanDirectoryPath(this.outputPath);

        const repo = new GitRepo(this.outputPath);
        await repo.clone(repoName,remote.url);

        if(!await repo.remoteBranchExists(remote.url,targetBranch)){
            const message = `Cannot delete remote branch ${targetBranch}. Branch does not exist on remote ${remote.url}`;
            throw message;
        }

        await repo.deleteRemoteBranch(repoName,targetBranch);
    }

    private configureRemote =  (remote: GitRemote): GitRemote => {
        let targetRemote = remote;
        targetRemote.branch = this.options.branch;
    
        const repoProps = getRepoPropsFromRemote(remote.url);
        if (this.options.https && repoProps.host == 'github.com') {
            console.info(chalk.white(`Using HTTPS URL for GitHub repo`));
            const remoteHttpUrl = createRepoUrlFromRemote(targetRemote.url);
            targetRemote.url =`${remoteHttpUrl}.git`
        }
        return targetRemote;
    }
   
    private validRemotes = (): Boolean => {
        if (!this.manifest.repo.remotes || this.manifest.repo.remotes.length === 0) {
            console.warn(chalk.yellowBright("Remotes manifest is missing 'remotes' configuration and is unable to push changes"));
            return false;
        }
        return true;
    }
}
