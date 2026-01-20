// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

/**
 * Supported programming languages for Azure Developer CLI projects
 */
export const SUPPORTED_LANGUAGES = ['python', 'js', 'ts', 'csharp', 'java', 'go'] as const;

export type SupportedLanguage = typeof SUPPORTED_LANGUAGES[number];
