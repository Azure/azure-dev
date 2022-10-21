// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// @ts-check // Check this file for typing issues (helps prevent mistakes in options passed)
/* eslint-disable no-undef */ // Ignore the fact that the engine (which is webpack) is unknown

// Good reference point:
// https://github.com/Microsoft/vscode/blob/main/extensions/shared.webpack.config.js

'use strict';

/* eslint-disable @typescript-eslint/no-var-requires */
const path = require('path');
const fs = require('fs/promises');
const CopyPlugin = require('copy-webpack-plugin');
const TerserPlugin = require('terser-webpack-plugin');
const BundleAnalyzerPlugin = require('webpack-bundle-analyzer').BundleAnalyzerPlugin;
/* eslint-enable @typescript-eslint/no-var-requires */

const debugWebpack = !!process.env.DEBUG_WEBPACK;

/** @type {import('webpack').Configuration} */ // Here's where we can get typing help even though it's JS
const config = {
    target: 'node', // vscode extensions run in a Node.js-context 📖 -> https://webpack.js.org/configuration/node/
    mode: 'none', // this leaves the source code as close as possible to the original (when packaging we set this to 'production')
    cache: true, // Makes 'watch' builds way faster after the first full build

    entry: './src/extension.ts', // the entry point of this extension, 📖 -> https://webpack.js.org/configuration/entry-context/
    output: {
        // the bundle is stored in the 'dist' folder (check package.json), 📖 -> https://webpack.js.org/configuration/output/
        path: path.resolve(__dirname, 'dist'),
        filename: 'extension.js',
        libraryTarget: 'commonjs2',
        devtoolModuleFilenameTemplate: '../[resource-path]'
    },
    devtool: 'inline-source-map',
    externals: {
        vscode: 'commonjs vscode' // the vscode-module is created on-the-fly and must be excluded. Add other modules that cannot be webpack'ed, 📖 -> https://webpack.js.org/configuration/externals/
    },
    resolve: {
        // support reading TypeScript and JavaScript files, 📖 -> https://github.com/TypeStrong/ts-loader
        extensions: ['.ts', '.js']
    },
    module: {
        rules: [
            {
                // Default TypeScript loader for .ts files
                test: /\.ts$/,
                exclude: /node_modules/,
                use: [
                    {
                        loader: 'ts-loader'
                    }
                ]
            },
            {
                test: /\.node$/,
                loader: 'node-loader',
            },
        ]
    },
    plugins: [
        // Copy some needed resource files from external sources
        new CopyPlugin({
            patterns: [
                './node_modules/open/xdg-open*', // This script isn't included in the webpack but is needed by `open` on certain systems, so copy it in
            ],
        }),
        {
            // Webpack does not preserve the execute permission on the above xdg-open script, so apply it again within the bundle
            apply: (compiler) => {
                compiler.hooks.afterEmit.tapPromise('AzCodeCopyWorkaround', async () => {
                    await fs.chmod('./dist/node_modules/open/xdg-open', '755');
                });
            },
        },
    ],
    optimization: {
        minimizer: [
            new TerserPlugin({
                terserOptions: {
                    /* eslint-disable @typescript-eslint/naming-convention */
                    // Keep class and function names so that stacks aren't useless and things like UserCancelledError work
                    keep_classnames: true,
                    keep_fnames: true,
                    /* eslint-enable @typescript-eslint/naming-convention */
                }
            }),
        ]
    },
    ignoreWarnings: [
        // Suppress some webpack warnings caused by dependencies
        {
            // Ignore a warning from `@vscode/extension-telemetry`
            module: /node_modules\/@vscode\/extension-telemetry/,
            message: /Can't resolve 'applicationinsights-native-metrics'/
        },
        (warning) => false, // No other warnings should be ignored
    ],
};

if (debugWebpack) {
    config.plugins.push(new BundleAnalyzerPlugin({ analyzerMode: 'static' }));
    console.log('Config:', config);
}

module.exports = config;

/* eslint-enable no-undef */
