import chalk from "chalk";
import del from "del";
import glob, { IOptions } from "glob";
import path from "path";
import fs from "fs/promises";
import { isDebug } from "../common/config";
import { exec, ExecOptions, spawn, SpawnOptionsWithoutStdio } from "child_process";

export const getGlobFiles = async (pattern: string, globOptions: IOptions): Promise<string[]> => {
    return new Promise((resolve, reject) => {

        if (isDebug()) {
            console.debug(`Searching for glob pattern ${pattern} within ${globOptions.cwd}`);
        }

        glob(pattern, globOptions, async (err, files) => {
            if (err) {
                return reject(err);
            }

            resolve(files);
        });
    });
}

export const copyFile = async (sourcePath: string, destPath: string) => {
    try {
        const destDirectoryPath = path.dirname(destPath);
        await ensureDirectoryPath(destDirectoryPath);
        await fs.copyFile(sourcePath, destPath);

        if (isDebug()) {
            console.debug(chalk.grey(`- Copied ${sourcePath} => ${destPath}`))
        }
    }
    catch (err) {
        console.error(chalk.red(`- ${err}`));
    }
}

export const ensureDirectoryPath = async (directoryPath: string) => {
    try {
        await fs.access(directoryPath);
    }
    catch (err) {
        await fs.mkdir(directoryPath, { recursive: true });
    }
}

export const isFilePath = async (filePath: string) => {
    try {
        const link = await fs.lstat(filePath);
        return link.isFile()
    }
    catch (err: any) 
    {
        console.warn(chalk.yellowBright(`- ${err.message}`));
    }
}

export const cleanDirectoryPath = async (directoryPath: string, cleanGit: boolean = true) => {
    if (isDebug()) {
        console.debug(chalk.yellow(`Cleaning output directory: ${directoryPath}`));
    }
    const patterns = cleanGit ? ["**"] : ["**", "!.git"];
    await del(patterns, { cwd: directoryPath, force: true, dot: true });
}

export const ensureRelativeBasePath  = (input : string) => {
    const basePath =`.${path.sep}`
    if(!input.startsWith(basePath) && !input.startsWith(`.${basePath}`)) {
        input = `${basePath}${input}`
    }
    else{
        if (isDebug()) {
            console.warn(chalk.yellowBright(` - ${input} already contains a relative base path.`));
        }
    }
    return input;
}

export const toArray = (data: Buffer): string[] => {
    if (!(data instanceof Buffer)) {
        throw new Error("Not an instance of buffer");
    }

    return data.toString().trim().split(/(?:\r\n|\r|\n)/g);
}

export const createRepoUrlFromRemote = (remoteUrl: string) => {
    if (!remoteUrl.startsWith("git@")) {
        return remoteUrl;
    }
    const regex = /git@(.*):(.*).git/g
    return remoteUrl.replace(regex, "https://$1/$2")
}

export interface RepoProps {
    host: string
    org: string
    repo: string
}

export const getRepoPropsFromRemote = (remoteUrl: string): RepoProps => {
    const regex = /git@(.+):(.+)\/(.+)\.git/g;
    const matches = regex.exec(remoteUrl);

    if (!matches || matches.length !== 4) {
        throw new Error(`Unable to determine repo properties from remote: ${remoteUrl}`)
    }

    return {
        host: matches[1],
        org: matches[2],
        repo: matches[3]
    };
}

export interface CommandOptions {
    cwd?: string
}

export const executeCommand = (command: string, options?: ExecOptions): Promise<string> => {
    if (isDebug()) {
        console.debug(chalk.grey(`Executing command: ${command}, cwd: ${options?.cwd}`));
    }

    return new Promise<string>((resolve, reject) => {
        exec(command, options, (error, stdout) => {
            if (error) {
                reject(error);
            }

            const output = stdout.toString().trim();
            if (isDebug()) {
                console.debug(chalk.gray(`output: ${output}`));
            }

            resolve(output);
        });
    })
}

export const spawnCommand = async (command: string, args: string[], options?: SpawnOptionsWithoutStdio): Promise<string[]> => {
    if (isDebug()) {
        console.debug(chalk.gray(`Spawning command: ${[command, ...args].join(" ")}, cwd: ${options?.cwd}`));
    }

    return new Promise<string[]>((resolve, reject) => {
        const process = spawn(command, args, options);

        const output: string[] = [];
        const errors: string[] = [];

        process.stdout.on('data', (data: Buffer) => {
            if (data) {
                output.push(...toArray(data));
            }
        });

        process.stderr.on("stderr <=", (data: Buffer) => {
            if (data) {
                errors.push(...toArray(data));
            }
        });

        process.on("error", (err: Error) => {
            errors.push(err.message);
        });

        process.on("close", (code) => {
            if (isDebug()) {
                console.debug(chalk.gray(`${command} process exited with code ${code}`));
                output.map(line => console.debug(chalk.gray(`output: ${line}`)));
                errors.map(line => console.debug(chalk.gray(`error: ${line}`)));
            }

            if (code === 0) {
                resolve(output);
            } else {
                reject(output);
            }
        })
    });
}

export const isStringNullOrEmpty = (value?: string) => {
    if (!value) {
        return true;
    }

    if (value.trim() === "") {
        return true;
    }

    return false;
}

export interface HeaderOptions {
    char?: string
    length?: number
    color?: chalk.Chalk
}

export const writeHeader = (value: string, options?: HeaderOptions) => {
    const settings: Required<HeaderOptions> = {
        char: "*",
        length: 60,
        color: chalk.white,
        ...options
    };

    const prefixLength = Math.ceil((settings.length - value.length - 2) / 2);
    const prefix = settings.char.repeat(prefixLength);
    const line = settings.char.repeat(prefixLength * 2 + value.length + 2);
    console.info(settings.color(line));
    console.info(settings.color(`${prefix} ${value} ${prefix}`));
    console.info(settings.color(line));
}

