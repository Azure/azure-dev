# AZD New Project Creation Orchestration Instructions

✅ **Agent Task List**  

1. Welcome user and confirm they want to create a new AZD project from scratch
2. Conduct user intent discovery to understand project requirements and constraints
3. Perform stack selection process to determine optimal technology approach
4. Conduct architecture planning to design Azure service mappings and infrastructure
5. Document comprehensive project specification in Application specification
6. Provide file generation guidance and next steps for implementation
7. Run full azd project validation and address all errors
8. Ensure project specification is fully up to date

📄 **Required Outputs**  

- Complete Application specification specification document with all project details
- Clear technology stack recommendation with rationale
- Azure service architecture design with component mappings
- File generation roadmap for implementation phase
- Project validation checklist for final verification
- Ready-to-implement project specification

🧠 **Execution Guidelines**  

**CRITICAL:** This tool orchestrates the complete new project creation flow. Start fresh and guide the user through each phase systematically. Create comprehensive documentation that serves as the blueprint for file generation and deployment.

## Phase 1: Project Initiation and Welcome

**Initial Setup:**

- Confirm this is a new project creation (no existing code or infrastructure)
- Verify the user wants to create an AZD-compatible Azure project
- Explain the process: "We'll discover your requirements, select the best technology stack, and create a complete project specification"
- Create new Application specification file to document the entire journey

**Prerequisites Check:**

- User has a clear idea of what application they want to build
- User has access to Azure subscription or is planning for Azure deployment
- User is ready to answer questions about their project requirements and constraints

## Phase 2: Requirements Discovery

**Conduct User Intent Discovery Process:**

Use the user intent discovery capability to understand:

- Project purpose and classification (POC, Development Tool, Production Application)
- Expected scale and user base requirements
- Budget and cost considerations
- Performance and availability expectations
- Timeline and deployment urgency

**Key Questions to Address:**

- What problem does this application solve?
- Who are the target users and how many?
- What are the critical success factors?
- Are there specific compliance or security requirements?
- What's the expected timeline for deployment?

**Documentation Target:**

Update Application specification with comprehensive "Project Requirements" section including all discovered intent and constraints.

## Phase 3: Technology Stack Selection

**Perform Stack Selection Process:**

Use the stack selection capability to determine:

- Team technical expertise and preferences
- Application characteristics and complexity
- Performance and scalability requirements
- Integration and workflow needs

**Stack Options to Evaluate:**

- **Containers**: For complex applications requiring infrastructure control
- **Serverless**: For event-driven, cost-optimized, simple deployments
- **Logic Apps**: For integration-heavy, workflow automation scenarios

**Selection Criteria:**

- Align with team expertise and operational preferences
- Match application performance and scaling requirements
- Support integration and business process needs
- Optimize for identified budget and cost constraints

**Documentation Target:**

Update Application specification with "Stack Selection" section documenting chosen stack and detailed rationale.

## Phase 4: Application Architecture Definition

**Project Specification Development:**

Based on user requirements and selected stack, define:

- **Application Components**: Break down the application into logical services/components
- **Component Responsibilities**: Define what each component does and its purpose
- **Data Requirements**: Identify data storage, processing, and flow needs
- **Integration Points**: Map external systems, APIs, and service dependencies
- **User Interaction Patterns**: Define how users interact with the application

**Architecture Planning Considerations:**

- Component communication patterns (REST APIs, messaging, events)
- Data persistence strategies (databases, file storage, caching)
- Authentication and authorization requirements
- Monitoring and logging needs
- Security and compliance requirements

**Documentation Target:**

Create detailed "Application Architecture" section in Application specification with component breakdown and specifications.

## Phase 5: Azure Service Architecture Planning

**Conduct Architecture Planning Process:**

Use the architecture planning capability to:

- Map each application component to optimal Azure services
- Design infrastructure organization and resource grouping
- Plan networking and security architecture
- Select appropriate database and messaging services
- Design containerization strategy for selected stack

**Service Selection Focus:**

- Leverage the selected stack (Containers/Serverless/Logic Apps) as primary guidance
- Choose services that align with performance and scale requirements
- Optimize for identified budget and cost constraints
- Ensure services support required integrations and compliance needs

**Infrastructure Design Elements:**

- Resource group organization
- Networking and connectivity approach
- Security and access control strategy
- Monitoring and observability setup
- Backup and disaster recovery considerations

**Documentation Target:**

Update Application specification with complete "Azure Service Mapping" and "Infrastructure Architecture" sections.

## Phase 6: Implementation Roadmap Creation

**Artifact Generation Planning:**

Based on the architecture design, prepare for comprehensive artifact generation:

- **Application Scaffolding Strategy**: Define starter code structure and framework setup
- **Infrastructure Planning**: Identify all required Azure resources and configurations
- **Containerization Approach**: Plan Docker configurations if applicable
- **Configuration Management**: Design azure.yaml structure and service mappings

**Development Workflow Setup:**

- CI/CD pipeline requirements and integration points
- Local development environment setup and testing
- Deployment and rollback procedures
- Monitoring and observability configuration

**Documentation Target:**

Add "Implementation Roadmap" section to Application specification with detailed artifact generation requirements and setup guidance.

## Phase 7: Validation and Next Steps

**Project Validation Preparation:**

Reference the project validation capability to prepare for:

- Azure.yaml configuration validation
- Bicep template structure verification
- Environment setup validation
- Deployment readiness assessment

**Next Steps Guidance:**

Provide clear direction for moving to implementation:

- "Your project specification is complete and documented in Application specification"
- "Next phase: Execute artifact generation to create your complete project structure"
- "Use the artifact generation capability to create application scaffolding, Docker configurations, infrastructure templates, and azure.yaml file"
- "After artifact generation, use project validation to ensure everything is properly configured"
- "Finally, run `azd up` to deploy your application to Azure"

**Final Documentation Structure:**

Ensure Application specification contains all required sections:

```markdown
# [Project Name] - AZD Architecture Plan

## Project Requirements
[User intent discovery results]

## Stack Selection
[Chosen stack and rationale]

## Application Architecture
[Component breakdown and specifications]

## Azure Service Mapping
[Component to Azure service mappings]

## Infrastructure Architecture
[Resource organization and networking design]

## Implementation Roadmap
[File generation and setup guidance]

## Validation Checklist
[Project validation requirements]
```

**Success Criteria:**

- User understands their project architecture completely
- All technical decisions are documented with clear rationale
- Implementation path is clear and actionable
- Project is ready for file generation phase
- Validation requirements are understood
