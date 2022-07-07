import chalk from "chalk";
import { Command } from "commander";
import figlet from "figlet";
import { DebugCommand } from "./debug";

export type ActionHandler = (...args: any[]) => void | Promise<void>

export class ProgramCommand extends Command {
    private actions: ActionHandler[] = [];

    constructor(name?: string) {
        super(name);
    }

    public createCommand = (name: string) => {
        const command = new ProgramCommand(name)
            .option("--debug", "When set writes verbose output to the console", false)
            .on("option:debug", async () => {
                await new DebugCommand(command.opts()).execute();
            });

        return command;
    }

    public asciiArt = (value: string, font?: string) => {
        console.log(chalk.cyanBright(figlet.textSync(value)));
        return this;
    }
}
