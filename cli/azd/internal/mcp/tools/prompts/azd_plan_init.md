# AZD Project Initialization Decision Tree Instructions

**TASK:** Analyze workspace contents to determine project state, classify as "new project" or "existing application", route users to appropriate workflow, and execute the selected workflow with proper tool orchestration.

**SUCCESS CRITERIA:**

- Workspace analysis completed with clear routing decision rationale
- User confirmation obtained for selected workflow path
- Chosen workflow executed completely (new project creation or application modernization)
- Professional application specification document created
- Validated AZD compatible project ready for deployment

**VALIDATION REQUIRED:**

- Workspace analysis correctly identifies application vs minimal content
- Routing decision aligns with workspace contents and user intentions
- Selected workflow completes successfully with all required components
- Generated project passes AZD validation requirements
- Application specification provides comprehensive project documentation

**COMPLETION CHECKLIST:**

- [ ] Analyze current workspace to determine project state and contents
- [ ] Classify workspace as "empty/minimal" or "existing application"
- [ ] Route user to appropriate workflow with confirmation
- [ ] Perform selected workflow with proper tool orchestration
- [ ] Update application specification throughout the workflow
- [ ] Validate final project readiness for deployment

## Critical Analysis and Routing

**Workspace Classification Logic:**

**Route to New Project Creation:**

- No programming language files found
- Only documentation and git files present
- Empty workspace or minimal placeholder content
- User explicitly wants to start from scratch

**Route to Application Modernization:**

- Application code files detected (any programming language)
- Framework configuration files present (package.json, requirements.txt, etc.)
- Docker files or containerization artifacts found
- Clear application structure and entry points

**Handle Existing AZD Projects:**

- If `azure.yaml` exists, determine update/refinement needs
- Check completeness of existing AZD configuration
- Route to appropriate maintenance workflow

**User Confirmation Process:**

- Present analysis results with detected technologies and recommendations
- Explain chosen workflow path and what it will accomplish
- Obtain explicit user confirmation before proceeding
- Handle ambiguous cases by presenting options and letting user choose

**Workflow Execution:**

- Execute chosen workflow systematically with proper tool orchestration
- Maintain application specification throughout the process
- Ensure all workflow steps complete successfully
- Validate final project before completion
