// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Brief historical pointers; the linked issues own the full, maintained procedures.
export const KNOWN_ISSUES = [
    {
        number: 10522,
        title: "GitHub release publishing failed with an expired pipeline credential",
        stage: "Publishing",
        observedIn: "1.36.0",
        symptom: "Creating the GitHub release and uploading artifacts returned HTTP 401: Bad credentials.",
        workaround: "Ask authorized pipeline owners to investigate authentication and rotate the credential if needed. " +
            "After checking partial publication and obtaining approval, retry only the failed steps on the existing run where safe.",
        caution: "A 401 alone does not prove expiration. Interactive gh login is not a fix for the pipeline's injected credential.",
        url: "https://github.com/Azure/azure-dev/issues/10522",
    },
    {
        number: 10523,
        title: "A closed duplicate WinGet PR blocked final version markers",
        stage: "WinGet and version.txt",
        observedIn: "1.36.0",
        symptom: "The intended package published, but ESRP tracked a closed duplicate PR as rejected. " +
            "PublishCLI failed and the final version-marker stage was skipped.",
        workaround: "Verify the release assets, published packages, and remaining work. Escalate to publishing support for reconciliation. " +
            "If only the markers remain, an approved operator can use a reviewed, dry-run-first, GA-only marker recovery " +
            "with original run metadata and no downgrades; see the issue for prerequisites.",
        caution: "Do not blindly retry WinGet submission. The cause of duplicate creation is unknown. " +
            "Manual marker recovery does not make the original pipeline green.",
        url: "https://github.com/Azure/azure-dev/issues/10523",
    },
];
