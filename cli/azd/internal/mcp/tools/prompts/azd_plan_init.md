# AZD Project Initialization Decision Tree Instructions

✅ **Agent Task List**  

1. Analyze current workspace to determine project state and contents
2. Classify workspace as "empty/minimal" or "existing application"
3. Route user to appropriate workflow: new project creation or application modernization
4. Confirm routing decision with user before proceeding
5. Perform the selected workflow with proper tool orchestration
6. Update application specification after each step within the workflow

📄 **Required Outputs**  

- Workspace analysis summary with routing decision rationale
- User confirmation of selected workflow path
- Complete execution of chosen workflow (new project or modernization)
- Professional project specification document (app-spec.md)
- Validated AZD-compatible project ready for deployment

🧠 **Execution Guidelines**  

**CRITICAL:** This tool serves as the entry point for AZD project initialization. It analyzes the workspace and routes users to the most appropriate workflow. Always confirm the routing decision with users before proceeding.

## Workspace Analysis and Classification

**Comprehensive Workspace Scan:**

Analyze the current directory and subdirectories for:

**Application Indicators (suggest modernization path):**

- Programming language files: `.js`, `.ts`, `.py`, `.cs`, `.java`, `.go`, `.php`, `.rb`, etc.
- Framework configuration files: `package.json`, `requirements.txt`, `pom.xml`, `Gemfile`, `go.mod`, `composer.json`
- Application entry points: `main.py`, `app.js`, `index.js`, `Program.cs`, `Main.java`, `main.go`
- Web application files: HTML, CSS, JavaScript files, template files
- Docker configurations: `Dockerfile`, `docker-compose.yml`, `.dockerignore`
- Build configurations: `Makefile`, `CMakeLists.txt`, build scripts
- Configuration files: `.env`, `appsettings.json`, `config.yaml`, etc.

**Minimal Content Indicators (suggest new project path):**

- Only documentation files: `README.md`, `CHANGELOG.md`, `LICENSE`, etc.
- Git files: `.gitignore`, `.git` directory
- Empty directories or placeholder files
- Basic configuration without dependencies: empty `package.json`, template files

**Existing AZD Project Indicators:**

- `azure.yaml` file exists
- `./infra/` directory with Bicep templates
- Project specification document (app-spec.md)

## Decision Logic and Routing

**Classification Rules:**

**Route to New Project Creation if:**

- No programming language files found
- Only documentation and git files present
- Empty workspace or minimal placeholder content
- User explicitly wants to start from scratch

**Route to Application Modernization if:**

- Application code files detected (any programming language)
- Framework configuration files present
- Docker files or containerization artifacts found
- Existing build or deployment configurations
- Clear application structure and entry points

**Handle Existing AZD Projects:**

- If `azure.yaml` exists, determine if this is an update/refinement workflow
- Check completeness of existing AZD configuration
- Route to appropriate maintenance or enhancement workflow

**Ambiguous Cases:**

- When workspace contains mixed content, present findings and let user choose
- If minimal code exists but unclear if it's a real application vs examples
- When existing AZD files are incomplete or outdated

## User Confirmation and Workflow Selection

**Present Analysis Results:**

After workspace scan, provide summary:

- "I found [X] application files including [languages/frameworks detected]"
- "The workspace appears to contain [existing application/minimal content]"
- "Based on this analysis, I recommend the [modernization/new project] workflow"

**Confirmation Questions:**

**For Modernization Path:**

- "I detected an existing application with [technologies found]. Would you like to modernize this application for Azure deployment using AZD?"
- "This will add Azure deployment capabilities while preserving your existing application structure."

**For New Project Path:**

- "The workspace appears empty or contains only documentation. Would you like to create a new AZD project from scratch?"
- "This will guide you through defining requirements and creating a complete new application."

**Alternative Option:**

- Always offer the alternative: "Or would you prefer to [create new project/modernize existing] instead?"

## Workflow Execution

**New Project Creation Workflow:**

If user confirms new project path:

- Perform new project creation process
- Guide through complete requirements gathering and architecture planning
- Create comprehensive project specification and implementation roadmap
- Reference appropriate file generation and validation processes

**Application Modernization Workflow:**

If user confirms modernization path:

- Perform application modernization process
- Analyze existing architecture and gather migration requirements
- Plan Azure service mapping and infrastructure design
- Create AZD-compatible project structure while preserving existing functionality

**Existing AZD Project Enhancement:**

If AZD project already exists:

- Review current project specification (app-spec.md) for completed work
- Identify gaps or areas needing updates
- Perform targeted improvements or additions
- Validate and test updated configuration

## Decision Tree Summary

```text
Workspace Analysis
    ├── Empty/Minimal Content Found
    │   ├── Confirm: New Project Creation? → Begin New Project Workflow
    │   └── User Override → Begin Modernization Workflow
    │
    ├── Application Code Found
    │   ├── Confirm: Modernize Existing? → Begin Modernization Workflow  
    │   └── User Override → Begin New Project Workflow
    │
    ├── Existing AZD Project Found
    │   ├── Complete Configuration → Offer Enhancement Options
    │   └── Incomplete Configuration → Resume/Fix Configuration
    │
    └── Ambiguous Content
        └── Present Options → User Selects → Begin Chosen Workflow
```

## Error Handling and Edge Cases

**Workspace Access Issues:**

- If unable to scan workspace, ask user to describe their project
- Provide manual selection options for workflow choice

**Mixed Content Scenarios:**

- Example code mixed with real application code
- Multiple unrelated applications in same workspace
- Legacy or deprecated code alongside current application

**User Uncertainty:**

- If user is unsure about their project type, provide guided questions
- Offer to start with discovery process to help determine appropriate path
- Allow workflow switching if initial choice proves incorrect

## Success Criteria

**Successful Routing Achieved When:**

- Workspace analysis accurately reflects actual content
- User understands and confirms the recommended workflow
- Selected workflow matches user's actual goals and project state
- Execution proceeds smoothly with appropriate tool orchestration
- Final result meets user's deployment and functionality requirements

**Quality Assurance:**

- Decision rationale is clearly documented
- User confirmation is explicit and informed
- Workflow execution follows established patterns
- Documentation maintains consistency across all paths
- Final validation confirms project readiness
