// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { ShellQuotedString, ShellQuoting } from "vscode";


export type NamedArgValue = string | number | boolean | ShellQuotedString | undefined;

export class CommandLineBuilder {
    private readonly args: ShellQuotedString[] = [];

    public static create(command: (string | ShellQuotedString), ...args: (string | ShellQuotedString | undefined)[]): CommandLineBuilder {
        const builder = new CommandLineBuilder();
        builder.withArg(command);

        if (args !== undefined) {
            for (const arg of args) {
                builder.withArg(arg);
            }
        }

        return builder;
    }

    public withArg(arg: string | ShellQuotedString | undefined): CommandLineBuilder {
        if (typeof (arg) === 'string') {
            if (arg) { // Quoted strings can be added as empty, but withArg will not allow an empty string arg
                this.args.push(
                    {
                        value: arg,
                        quoting: ShellQuoting.Escape
                    }
                );
            }
        } else if (arg !== undefined) {
            this.args.push(arg);
        }

        return this;
    }

    public withArgs(args: string | string[] | ShellQuotedString[] | undefined): CommandLineBuilder {
        if (typeof (args) === 'string') {
            for (const arg of args.split(' ')) {
                this.withArg(arg);
            }
        } else if (args) {
            for (const arg of args) {
                this.withArg(arg);
            }
        }

        return this;
    }

    public withFlagArg(name: string, value: boolean | undefined): CommandLineBuilder {
        if (value) {
            this.withArg(name);
        }

        return this;
    }

    public withNamedArg(name: string, value: string | number | boolean | ShellQuotedString | undefined, options?: { assignValue?: boolean }): CommandLineBuilder {
        if (typeof (value) === 'string' || typeof(value) === 'number' || typeof(value) === 'boolean') {
            if (options && options.assignValue) {
                this.withArg(
                    {
                        value: `${name}=${value}`,
                        quoting: ShellQuoting.Strong
                    }
                );
            } else {
                this.withArg(name);
                this.withArg(
                    {
                        value: value.toString(),
                        quoting: ShellQuoting.Strong // The prior behavior was to quote
                    }
                );
            }
        } else if (value !== undefined) {
            this.withArg(name);
            this.withArg(value);
        }

        return this;
    }

    public withNamedArgs(name: string, values: NamedArgValue | NamedArgValue[], options?: { assignValue?: boolean }): CommandLineBuilder {
        for (const value of (Array.isArray(values) ? values : [ values ])) {
            this.withNamedArg(name, value, options);
        }   

        return this;
    }

    public withQuotedArg(value: string | undefined): CommandLineBuilder {
        if (value !== undefined) {
            this.withArg(
                {
                    value: value,
                    quoting: ShellQuoting.Strong
                }
            );
        }

        return this;
    }

    public withKeyValueArgs(name: string, values: { [key: string]: string | ShellQuotedString | undefined } | undefined): CommandLineBuilder {
        if (values !== undefined) {
            for (const key of Object.keys(values)) {
                if (typeof (values[key]) === 'string') {
                    this.withArg(name);
                    this.withArg(
                        {
                            value: `${key}=${values[key]}`,
                            quoting: ShellQuoting.Strong // The prior behavior was to quote
                        }
                    );
                } else if (values[key] !== undefined) {
                    this.withArg(name);
                    this.withArg(values[key]);
                }
            }
        }

        return this;
    }

    public withArrayArgs<T extends { toString(): string }>(name: string, values: T[] | undefined, formatter?: (value: T) => string | ShellQuotedString): CommandLineBuilder {
        formatter = formatter || ((value: T): string => value.toString());

        if (values !== undefined) {
            for (const value of values) {
                if (value !== undefined) {
                    const formatted = formatter(value);
                    if (typeof (formatted) === 'string') {
                        this.withArg(name);
                        this.withArg(
                            {
                                value: formatted,
                                quoting: ShellQuoting.Strong // The prior behavior was to quote
                            }
                        );
                    } else if (formatted !== undefined) {
                        this.withArg(name);
                        this.withArg(formatted);
                    }
                }
            }
        }

        return this;
    }

    public build(): string {
        return this.args.map(arg => {
            return arg.quoting === ShellQuoting.Strong ? `"${arg.value}"` : arg.value;
        }).join(' ');
    }

    public buildShellQuotedStrings(): ShellQuotedString[] {
        return this.args;
    }
}
