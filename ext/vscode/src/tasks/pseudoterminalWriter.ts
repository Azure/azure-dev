// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

export type WriterFont = 'default' | 'bold';

const fonts: { [key in WriterFont]: string } = {
    'default': '0m',
    'bold': '0;1m'
};

export class PseudoterminalWriter {
    constructor(private readonly writeToTerminal: (output: string) => void) {
    }

    public write(output: string, font: WriterFont = 'default'): void {
        output = output.replace(/\r?\n/g, '\r\n'); // The carriage return (/r) is necessary or the pseudoterminal does not return back to the start of line
        this.writeToTerminal(`\x1b[${fonts[font]}${output}\x1b[0m`);
    }

    public writeLine(output: string, font?: WriterFont): void {
        this.write(`${output}\r\n`, font); // The carriage return (/r) is necessary or the pseudoterminal does not return back to the start of line
    }
}
