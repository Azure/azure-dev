/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See LICENSE.md in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { azExtEslintRecommendedTypeChecked } from '@microsoft/vscode-azext-eng/eslint'; // Other configurations exist
import { defineConfig } from 'eslint/config';

export default defineConfig([
    azExtEslintRecommendedTypeChecked,
    {
        rules: {
        'header/header': [
            'error',
            {
                header: {
                    commentType: 'line',
                    lines: [
                        {
                            pattern: /Copyright.*Microsoft/,
                            template: ' Copyright (c) Microsoft Corporation. All rights reserved.',
                        },
                        {
                            pattern: /LICENSE/i,
                            template: ' Licensed under the MIT License.',
                        },
                    ],
                },
                trailingEmptyLines: {
                    minimum: 2,
                },
            },
        ],
    },
    }
]);
