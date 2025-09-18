/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See LICENSE.md in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { FlatCompat } from "@eslint/eslintrc";
import js from "@eslint/js";
import typescriptEslint from "@typescript-eslint/eslint-plugin";
import tsParser from "@typescript-eslint/parser";
import { defineConfig } from "eslint/config";
import path from "node:path";
import { fileURLToPath } from "node:url";

/* eslint-disable @typescript-eslint/naming-convention */
const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const compat = new FlatCompat({
    baseDirectory: __dirname,
    recommendedConfig: js.configs.recommended,
    allConfig: js.configs.all
});

export default defineConfig([{
    extends: compat.extends(
        "eslint:recommended",
        "plugin:@typescript-eslint/eslint-recommended",
        "plugin:@typescript-eslint/recommended",
    ),

    plugins: {
        "@typescript-eslint": typescriptEslint,
    },

    languageOptions: {
        parser: tsParser,
        ecmaVersion: 6,
        sourceType: "module",

        parserOptions: {
            project: "tsconfig.json",
        },
    },

    rules: {
        "@typescript-eslint/naming-convention": ["warn", { // Naming is enforced with some exceptions below
            // Names should be either camelCase or PascalCase, both are extensively used throughout this project
            selector: "default",
            format: ["camelCase", "PascalCase"],
        }, { // const variables can also have UPPER_CASE
                selector: "variable",
                modifiers: ["const"],
                format: ["camelCase", "PascalCase", "UPPER_CASE"],
            }, { // private class properties can also have leading _underscores
                selector: "classProperty",
                modifiers: ["private"],
                format: ["camelCase", "PascalCase"],
                leadingUnderscore: "allow",
            }],

        "@typescript-eslint/no-floating-promises": "warn", // Floating promises are bad, should do `void thePromise()`
        "@typescript-eslint/no-inferrable-types": "off", // This gets upset about e.g. `const foo: string = 'bar'` because it's obvious that it's a string; it doesn't matter enough to enforce

        "@typescript-eslint/no-unused-vars": ["warn", {
            // As a function parameter, unused parameters are allowed
            args: "none",
        }],

        curly: "warn", // May have been a mistake to include a `{curly}` inside a template string, you might mean `${curly}`
        eqeqeq: "warn", // Should use `===`, not `==`, nearly 100% of the time
        "no-extra-boolean-cast": "off", // We !!flatten a lot of things into booleans this way
        "no-throw-literal": "warn", // Elevate this from suggestion to warning
        semi: "warn",
    },
}]);
/* eslint-enable @typescript-eslint/naming-convention */
