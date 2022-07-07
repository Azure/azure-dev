import { setDebug } from "../common/config";
import { RepomanCommand, RepomanCommandOptions } from "../models";
import { getTable } from "console.table";

export class DebugCommand implements RepomanCommand {
    constructor(private options: RepomanCommandOptions) {
        setDebug(options.debug);
    }

    public execute(): Promise<void> {
        const values = Object.keys(this.options).map((key, value) => ({ key, value: this.options[key] }));
        console.debug("=========================================");
        console.debug("COMMAND OPTIONS")
        console.debug("=========================================");
        console.debug(getTable(values));
        return Promise.resolve();
    }
}