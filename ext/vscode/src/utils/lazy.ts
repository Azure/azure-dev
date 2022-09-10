// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

export class Lazy<T> {
    private isValueCreated: boolean = false;
    private val: T | undefined;

    public constructor(private readonly valueFactory: () => T) {
    }

    public get hasValue(): boolean {
        return this.isValueCreated;
    }

    public clear(): void {
        this.isValueCreated = false;
    }

    public get value(): T {
        if (this.isValueCreated) {
            return this.val as T;
        }

        this.val = this.valueFactory();
        this.isValueCreated = true;
        return this.val;
    }
}

export class AsyncLazy<T> {
    private isValueCreated: boolean = false;
    private val: T | undefined;
    private valuePromise: Promise<T> | undefined;

    // eslint-disable-next-line @typescript-eslint/naming-convention
    public constructor(private readonly valueFactory: () => Promise<T>, private valueLifetime?: number) {
    }

    public get hasValue(): boolean {
        return this.isValueCreated;
    }

    public clear(): void {
        this.isValueCreated = false;
        this.valuePromise = undefined;
    }

    public async getValue(): Promise<T> {
        if (this.isValueCreated) {
            return this.val as T;
        }

        const meStartedFactory = this.valuePromise === undefined;

        if (meStartedFactory) {
            this.valuePromise = this.valueFactory();
        }

        // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
        const result = await this.valuePromise!;

        if (meStartedFactory) {
            this.val = result;
            this.valuePromise = undefined;
            this.isValueCreated = true;

            if (this.valueLifetime) {
                const timer = setTimeout(() => {
                    this.isValueCreated = false;
                    this.val = undefined;
                }, this.valueLifetime);

                // Do not hold the process waiting for the lifetime of the value to expire.
                timer.unref(); 
            }
        }

        return result;
    }
}
