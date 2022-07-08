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
