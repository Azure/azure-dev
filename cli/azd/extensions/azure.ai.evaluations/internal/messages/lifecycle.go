// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import "fmt"

// GenerationIncomplete distinguishes partial outcomes from completed generation.
func GenerationIncomplete() string {
	return "\nGeneration did not fully complete. Successful artifacts were kept; nothing was rolled back.\n"
}

// GenerationRetained reports a downloaded and declared successful artifact.
func GenerationRetained(kind, name, version string) string {
	return fmt.Sprintf("  Kept %s %s (downloaded and declared in the catalog).\n",
		kind, ArtifactDescription(name, version))
}

// GenerationCatalogFailed distinguishes collection from a failed catalog update.
func GenerationCatalogFailed(kind, name, version string) string {
	return fmt.Sprintf("  Kept downloaded %s %s, but its catalog update failed.\n",
		kind, ArtifactDescription(name, version))
}

// GenerationDidNotFinish labels the artifact whose generation or collection failed.
func GenerationDidNotFinish(kind string, err error) string {
	return fmt.Sprintf("  %s did not finish: %v\n", kind, err)
}

// GenerationRecovery names a non-billing inspection or collection command.
func GenerationRecovery(command, guidance string) string {
	return fmt.Sprintf("  Inspect or collect without regenerating: %s\n  %s\n", command, guidance)
}

// GenerationRetryGuidance keeps a retry scoped to the failed artifact.
func GenerationRetryGuidance(kind string, hasJob bool) string {
	prefix := "Check for a submitted job before retrying."
	if hasJob {
		prefix = "If the job is still running or succeeded, use job show again; do not regenerate it."
	}
	return prefix + fmt.Sprintf(
		" If a new generation is needed, repeat the original generate command with --%s only "+
			"(remove the other artifact selector and its flags), preserving its inputs. "+
			"Do not regenerate artifacts that succeeded.", kind)
}

// GenerationDeclarationOnly explains why generation did not change an existing eval.
func GenerationDeclarationOnly(kind, name string) string {
	return fmt.Sprintf(
		"Generated %s %q is declared in the catalog, but no existing eval references it. "+
			"Generation does not attach artifacts to an eval. Use init to create a new eval, "+
			"or explicitly edit a compatible eval's references.", kind, name)
}

// GeneratedDatasetNotForTraces explains the mutually exclusive data sources.
func GeneratedDatasetNotForTraces() string {
	return "A trace-backed eval cannot consume a dataset; keep it unchanged and create a separate dataset-backed eval."
}

// CreateDependenciesRetained reports partial reconciliation without suggesting rollback.
func CreateDependenciesRetained(retry string) string {
	return "Evaluation creation did not complete. Successfully resolved dependencies were kept; nothing was rolled back.\n" +
		"After fixing the reported error, retry without republishing unchanged artifacts: " + retry + "\n"
}
