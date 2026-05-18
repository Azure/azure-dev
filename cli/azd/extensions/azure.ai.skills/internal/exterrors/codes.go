// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

// Error codes for skill validation.
//
// These are usually paired with [Validation] when user input, files,
// or option combinations fail validation specific to the skill commands.
const (
	// CodeInvalidSkillName is used when <name> does not match the
	// service-documented (or fallback) skill-name regex.
	CodeInvalidSkillName = "invalid_skill_name"
	// CodeInvalidSkillFile is used when --file points to a missing,
	// unreadable, or unsupported file, or when SKILL.md front matter
	// fails to parse.
	CodeInvalidSkillFile = "invalid_skill_file"
	// CodeSkillArchiveUnsafe is used when a downloaded gzip archive
	// contains an unsafe entry (zip-slip, symlink, oversized, etc.).
	CodeSkillArchiveUnsafe = "skill_archive_unsafe"
	// CodeSkillOutputCollision is used when `skill download` would
	// overwrite an existing file and --force was not supplied.
	CodeSkillOutputCollision = "skill_output_collision"
)

// Error codes shared across the extension surface.
const (
	CodeConflictingArguments   = "conflicting_arguments"
	CodeInvalidParameter       = "invalid_parameter"
	CodeMissingProjectEndpoint = "missing_project_endpoint"
	CodeMissingRequiredField   = "missing_required_field"
	CodeMissingForceFlag       = "missing_force_flag"
	CodeSkillNotFound          = "skill_not_found"
	CodeSkillAlreadyExists     = "skill_already_exists"
	CodeSkillNoPackage         = "skill_no_package"
)

// Error codes for auth.
const (
	//nolint:gosec // error code identifier, not a credential
	CodeCredentialCreationFailed = "credential_creation_failed"
)

// Operation names for [ServiceFromAzure] errors.
// These are prefixed to the Azure error code (e.g., "create_skill.NotFound").
const (
	OpCreateSkill   = "create_skill"
	OpUpdateSkill   = "update_skill"
	OpDeleteSkill   = "delete_skill"
	OpGetSkill      = "get_skill"
	OpListSkills    = "list_skills"
	OpDownloadSkill = "download_skill"
)
