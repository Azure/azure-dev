# AZD Stack Selection and Recommendation Instructions

**TASK:** Assess team expertise, application characteristics, and project requirements through targeted questions to select the optimal single technology stack (Containers, Serverless, or Logic Apps) with clear rationale.

**SUCCESS CRITERIA:**

- Single stack recommendation provided (Containers, Serverless, or Logic Apps)
- Clear rationale documented based on team capabilities and application needs
- Application specification updated with "Stack Selection" section
- Next steps guidance provided for the chosen stack

**VALIDATION REQUIRED:**

- Stack selection aligns with team expertise and preferences
- Chosen stack supports application characteristics and requirements
- Decision rationale is clearly documented and defensible
- Application specification properly updated with selection details
- Recommendation sets up successful architecture planning phase

**COMPLETION CHECKLIST:**

- [ ] Review existing application specification for project requirements and context
- [ ] Assess team expertise and technical preferences through targeted questions
- [ ] Evaluate performance and scalability requirements for the application
- [ ] Determine workflow and integration complexity needs
- [ ] Select optimal single stack with clear rationale
- [ ] Document stack selection and reasoning in application specification
- [ ] User has confirmed stack recommendation

## Critical Selection Criteria

**Team Expertise Assessment:**

- Experience with containerization technologies (Docker)
- Comfort with event-driven or function-based programming
- Experience with workflow automation and business process tools
- Programming language and framework preferences
- Operational preferences (infrastructure control vs managed services)

**Application Characteristics Analysis:**

- Traffic patterns (steady vs variable/unpredictable)
- Performance requirements (cold start sensitivity, response times)
- Runtime characteristics (continuous vs event-driven)
- Computational complexity (lightweight vs heavy workloads)

**Integration and Workflow Requirements:**

- System integration complexity (connecting APIs, data sources, legacy systems)
- Business workflow needs (multi-step processes, approvals, decision points)
- File processing and document workflow requirements
- Visual workflow design preferences vs code-based solutions

**Stack Selection Logic:**

**Containers Stack:**

- Team has Docker experience OR wants infrastructure control
- Complex dependencies or custom runtime requirements
- Need consistent performance with minimal cold starts
- Building traditional web applications, APIs, or microservices

**Serverless Stack:**

- Team prefers code-focused over infrastructure management
- Event-driven application or variable traffic patterns
- Cost optimization priority with unpredictable usage
- Simple to moderate complexity applications

**Logic Apps Stack:**

- Integration-heavy scenarios with many external systems
- Business process automation and workflow requirements
- Visual workflow design preference
- Complex multi-step business processes with approvals
