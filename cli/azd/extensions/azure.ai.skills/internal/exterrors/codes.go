// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Skill-specific validation error codes.
const (
	CodeInvalidSkillName     = "invalid_skill_name"
	CodeInvalidSkillFile     = "invalid_skill_file"
	CodeSkillArchiveUnsafe   = "skill_archive_unsafe"
	CodeSkillOutputCollision = "skill_output_collision"
)

// Codes shared across the extension surface.
const (
	CodeConflictingArguments    = "conflicting_arguments"
	CodeInvalidParameter        = "invalid_parameter"
	CodeMissingProjectEndpoint  = "missing_project_endpoint"
	CodeMissingRequiredField    = "missing_required_field"
	CodeMissingForceFlag        = "missing_force_flag"
	CodeProjectManifestNotFound = "project_manifest_not_found"
	CodeSkillServiceConflict    = "skill_service_conflict"
)

const (
	//nolint:gosec // error code identifier, not a credential
	CodeCredentialCreationFailed = "credential_creation_failed"
)

// Operation names for ServiceFromAzure errors. Prefixed to the Azure code,
// e.g. "create_skill.NotFound".
const (
	OpCreateSkill    = "create_skill"
	OpUpdateSkill    = "update_skill"
	OpDeleteSkill    = "delete_skill"
	OpGetSkill       = "get_skill"
	OpListSkills     = "list_skills"
	OpDownloadSkill  = "download_skill"
	OpReconcileSkill = "reconcile_skill"
)
