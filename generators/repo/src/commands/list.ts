import chalk from "chalk";
import path from "path";
import yaml from "yamljs";
import { RepomanCommand, RepomanCommandOptions, RepoManifest } from "../models";
import { getGlobFiles } from "../common/util";
import { getTable } from "console.table";
import { isDebug } from "../common/config";

export interface ListCommandOptions extends RepomanCommandOptions {
    path: string
    format: string
}

export class ListCommand implements RepomanCommand {
    private sourcePath: string

    constructor(private options: ListCommandOptions) {
        this.sourcePath = path.resolve(path.normalize(options.path));
    }

    public execute = async () => {
        if (isDebug()) {
            console.debug(chalk.grey(`sourcePath: ${this.sourcePath}`));
        }

        const isJsonFormat = this.options.format === "json";

        if (!isJsonFormat) {
            console.info(chalk.green(`Searching for repo templates within path ${this.sourcePath}`));
            console.info();
        }
        const files = await getGlobFiles("**/repo.yaml", { cwd: this.sourcePath, dot:true });

        if (!isJsonFormat) {
            console.info(chalk.cyan(`Found ${files.length} templates within search path`));
            console.info();
        }

        const projects = files.map(filePath => ({
            projectPath: path.dirname(filePath),
            templatePath: filePath,
            template: yaml.load(filePath) as RepoManifest
        }));

        if (isJsonFormat) {
            console.log(JSON.stringify(projects, null, 2));
        } else {
            const rows = projects.map(p => ({
                name: p.template.metadata.name,
                projectPath: p.projectPath,
                templatePath: p.templatePath,
                remotes: p.template.repo.remotes.map(r => r.name),
                includeProjectAssets: p.template.repo.includeProjectAssets,
            }));

            console.log(getTable(rows));
        }
    }
}