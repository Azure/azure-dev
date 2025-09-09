# AZD Stack Selection and Recommendation Instructions

✅ **Agent Task List**  

1. Review existing Application specification for project requirements and user intent findings
2. Assess team expertise and technical preferences through targeted questions
3. Evaluate performance and scalability requirements for the application
4. Determine workflow and integration complexity needs
5. Select the optimal single stack: Containers, Serverless, or Logic Apps
6. Document stack selection rationale in Application specification

📄 **Required Outputs**  

- Single stack recommendation: **Containers**, **Serverless**, or **Logic Apps**
- Clear rationale for stack selection based on user responses
- Updated Application specification with "Stack Selection" section
- Next steps guidance for the chosen stack

🧠 **Execution Guidelines**  

**CRITICAL:** Always check for existing Application specification first to understand project context. Use generic, non-Azure-specific language when asking questions. Focus on application characteristics and team capabilities rather than specific Azure services.

## Phase 1: Context Review and Setup

**Review Existing Information:**

- Check if Application specification exists and review "Project Requirements" section
- Note project type (POC/Development/Production), scale category, and architectural preferences
- Use existing findings to tailor questions and skip redundant inquiries

**If no prior context exists, gather basic project understanding:**

- "What type of application are you building?" (web app, API, data processing, etc.)
- "What's the primary function of your application?"

## Phase 2: Team Expertise and Technical Preference Assessment

**Development Experience Questions:**

- "What's your team's experience level with containerization technologies like Docker?"
- "Have you worked with event-driven or function-based programming before?"
- "Does your team have experience with workflow automation or business process tools?"
- "What programming languages and frameworks is your team most comfortable with?"

**Operational Preferences:**

- "Do you prefer to have control over the underlying infrastructure, or would you rather focus purely on code?"
- "How comfortable is your team with managing deployments and scaling decisions?"
- "Would you prefer predictable costs or pay-per-use pricing that scales with demand?"

## Phase 3: Application Characteristics Analysis

**Performance and Scalability Questions:**

- "Does your application need to handle sudden spikes in traffic or maintain steady performance?"
- "How important are cold start times? Do you need instant response to requests?"
- "Will your application run continuously or respond to specific events/triggers?"
- "Do you expect heavy computational workloads or primarily lightweight request processing?"

**Integration and Workflow Questions:**

- "Does your application primarily connect different systems, APIs, or data sources together?"
- "Are you building business workflows that involve multiple steps, approvals, or decision points?"
- "Do you need to integrate with many external services, databases, or legacy systems?"
- "Is your application more about processing data/requests or orchestrating complex business processes?"

**Complexity and Maintenance Questions:**

- "How complex is your business logic? Is it straightforward request-response or multi-step processes?"
- "Do you need to handle file processing, document workflows, or data transformations?"
- "Will you need visual workflow designers or are you comfortable with code-based solutions?"

## Phase 4: Stack Selection Logic

**Use this decision framework to recommend a single stack:**

### Containers Stack Recommendation

**Choose when:**

- Team has Docker/containerization experience OR wants infrastructure control
- Application has complex dependencies or custom runtime requirements
- Need consistent performance with minimal cold starts
- Building traditional web applications, APIs, or microservices
- Require custom scaling logic or persistent connections
- Application runs continuously or has predictable traffic patterns

**Key Indicators:**

- "We want control over our deployment environment"
- "We have complex dependencies that need specific configurations"
- "Performance consistency is critical for our users"
- "We're building a traditional web app or API service"

### Serverless Stack Recommendation

**Choose when:**

- Team prefers to focus on code over infrastructure management
- Application is event-driven or has variable/unpredictable traffic
- Cost optimization is important (pay only for usage)
- Building simple APIs, data processing, or reactive applications
- Quick development and deployment cycles are prioritized
- Automatic scaling without configuration is desired

**Key Indicators:**

- "We want to focus on writing code, not managing servers"
- "Our traffic is unpredictable or event-based"
- "We want to minimize operational overhead"
- "We're building functions that respond to specific triggers"

### Logic Apps Stack Recommendation

**Choose when:**

- Application is primarily about connecting systems and orchestrating workflows
- Team needs visual workflow design capabilities
- Building business process automation or integration solutions
- Multiple external API integrations are required
- Non-developers need to understand or modify workflows
- Complex business rules and approval processes are involved

**Key Indicators:**

- "We're connecting multiple existing systems together"
- "We need to automate business processes with multiple steps"
- "We have many external APIs and services to integrate"
- "Non-technical team members need to understand the workflow"
- "We're building automation that involves approvals or decision trees"

## Phase 5: Final Selection and Documentation

**Selection Process:**

1. Score each stack based on user responses (internal agent logic)
2. Select the highest-scoring stack
3. If tied, prefer in order: Logic Apps (for integration-heavy), Containers (for performance-critical), Serverless (for simplicity)

**Required Documentation in Application specification:**

Create or update with "Stack Selection" section:

```markdown
## Stack Selection

### Recommended Stack: [CONTAINERS/SERVERLESS/LOGIC APPS]

### Selection Rationale
- **Team Expertise:** [Summary of team capabilities and preferences]
- **Performance Requirements:** [Key performance and scalability factors]
- **Application Characteristics:** [Primary application type and complexity]
- **Integration Needs:** [External systems and workflow requirements]

### Key Decision Factors
- [List 3-4 main reasons why this stack was chosen]
- [Include specific user responses that influenced the decision]

### Next Steps
- [Specific guidance for proceeding with the chosen stack]
- [Get the specific Azure resources required for this stack]
- [Architecture planning will map these resources to your application components]
```

**Conversation Closure:**

- Summarize the selection: "Based on your requirements, I recommend the **[Stack Name]** stack because..."
- Explain the key benefits: "This choice will give you..."
- Provide confidence: "This stack aligns well with your team's expertise in..."
- Offer next steps: "The next phase will define the specific Azure resources for your chosen stack."

**Stack-Specific Next Steps Guidance:**

*Containers:*

- "We'll configure Container Apps environment and registry for your containerized applications"
- "Each application component will get its own Container App instance"
- "Consider container orchestration, networking, and scaling requirements"

*Serverless:*

- "We'll set up Azure Functions with appropriate triggers and bindings"
- "Plan for event-driven architecture with the baseline monitoring and configuration services"
- "Determine function hosting plans and scaling strategies"

*Logic Apps:*

- "We'll design workflows using Logic Apps with visual workflow designer"
- "Map out integration points with external systems and APIs"
- "Leverage the baseline services for configuration, secrets, and monitoring"

**Resource Planning Notes:**

All stacks will include the standard baseline resources plus the stack-specific compute resources. Retrieve the complete resource definitions for architecture planning.
