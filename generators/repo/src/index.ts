import chalk from 'chalk';
import figlet from 'figlet';
import { ProgramCommand } from './commands/command';
import { GenerateCommand, GenerateCommandOptions } from './commands/generate';
import { ListCommand, ListCommandOptions } from './commands/list';

export const main = async () => {
    const program = new ProgramCommand()
        .name("repoman")
        .usage("[command] [options]")
        .description("CLI for generating sample repos")
        .version("0.0.1")
        .addHelpText('beforeAll', chalk.cyanBright(figlet.textSync("repoman")));

    // List
    program
        .command("list")
        .description("Lists the repo templates available within the specified path")
        .requiredOption("-p --path <path>", "The path to search", ".")
        .option("-f --format <format>", "Specify the output format. json or text", "text")
        .action(async (options: ListCommandOptions) => {
            await new ListCommand(options).execute()
        });

    // Generate
    program
        .command("generate")
        .description("Generates a new repo based on a template configuration")
        .requiredOption("-o --output <output>", "The output path for the generated template")
        .requiredOption("-s, --source <source>", "The template source location", ".")
        .requiredOption("-t --templateFile <template>", "The repo template manifest location", "./repo.yaml")
        .option("-u --update", "When set will commit and push changes to the specified remotes & branches", false)
        .option("-h --https", "When set will generate HTTPS remote URLs for GitHub repos (useful for using a HTTPS PAT when updating)", false)
        .option("-e --fail-on-update-error", "When set will fail on remote update errors", false)
        .option("-r --remote <targetRemote>", "The remote name used while committing back to the target repos")
        .option("-b --branch <targetBranch>", "The target branch name for committing back to the target repos")
        .option("-m --message <message>", "Custom commit message used for committing back to the target repos")
        .option("--resultsFile <resultsFile>", "When specified writes markdown results to a file")
        .action(async (options: GenerateCommandOptions) => {
            await new GenerateCommand(options).execute();
        });

    await program.parseAsync(process.argv);
};

main();
