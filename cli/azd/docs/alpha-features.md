# Alpha Features

The Azure Developer CLI includes a `alpha feature toggle` which allows the cli to enable functionality in `alpha` mode (experimental). This document provides the feature design for it.

## Main requirement

As a customer, I can explicitly ask `azd` to unblock and show any _alpha_ functionality. The features becoming available are not finalized, so they can be changed or removed in future releases.

The strategy to ask `azd` to `toggle` between modes is:

- Enable _all_ experimental features: `azd config set alpha.all on`
- Enable _specific_ experimental feature: `azd config set alpha.featureName on`
- Disable _all_ experimental features: `azd config unset alpha.all` or `azd config set alpha.all off`
- Disable _specific_ experimental feature: `azd config unset alpha.featureName` or `azd config set alpha.featureName off`

## Alpha feature

All features start as alpha features (e.g., experimental). In this phase, the goal is to receive sufficient usage to get meaningful feedback around the feature’s design, functionality and user experience.

### Definition

-	These features are under active development.
-	Hidden behind a feature flag which interested users must explicitly opt into (these flags should be documented in the help text/docs of the product for easy discovery).
-	There are no guarantees about the long-term stability or supportability of experimental features (may contain bugs or expose bugs if used).
-	No commitment by the team that the feature is something we plan to advance to preview or stable stage (it’s an experiment).
-	A reasonable outcome of an experiment is “it didn’t work” and we rip out the code.
-	Recommended for non-business-critical uses because of potential for incompatible changes in subsequent releases

### Advancement criteria (how to reach preview).

-	The feature has been properly spec’d (initial draft) and approved by PM/eng/design.
-	PM + engineering team has had a formal review meeting to sign off on feature advancement to next phase.
-	Feature is documented on DevHub + there is help text in product.
-	We’ve received signal that the UX is successful via sufficient user feedback (could just be via internal feedback; depends on feature).


## Announcing in-preview features

The Azure Developer CLI can list available experimental features running:

> azd config list-alpha

### Changelog notes

Customer can refer to the release notes to discover changes for the list of experiments.
