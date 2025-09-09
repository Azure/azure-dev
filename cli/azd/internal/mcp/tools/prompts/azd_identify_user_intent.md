# AZD Project Intent and Requirements Discovery Instructions

✅ **Agent Task List**  

1. Engage user with conversational questions to understand project purpose and stage
2. Determine application scale requirements and production readiness
3. Identify budget and cost constraint considerations  
4. Understand architectural and technology stack preferences
5. Classify project into appropriate categories for optimal Azure service recommendations
6. Document findings in Application specification under "Project Requirements" section

📄 **Required Outputs**  

- Project classification: POC, Development Tool, or Production Application
- Scale category: Small, Medium, or Large (for production applications)
- Budget constraint level: Cost-Optimized, Balanced, or Performance-Focused
- Architectural preference: Containers, Serverless, Hybrid, or No Preference
- Comprehensive requirements summary documented in Application specification

🧠 **Execution Guidelines**  

**CRITICAL:** Ask questions conversationally, one at a time or in small groups. Adapt follow-up questions based on user responses. Don't overwhelm with a long questionnaire.

## Phase 1: Project Purpose and Stage Discovery

Start with foundational questions to understand the project's intent:

**Primary Questions:**

- "What's the main purpose of this application? Are you building a proof of concept, a development/internal tool, or planning for a production application?"
- "Who is the intended audience for this application?" (Internal team, external customers, specific user group)
- "What problem does this application solve?"

**Follow-up Questions (based on response):**

*If POC/Prototype:*

- "How long do you expect this proof of concept to run?"
- "Will this potentially evolve into a production application?"
- "Are you primarily focused on demonstrating functionality or testing performance?"

*If Development Tool:*

- "How many developers or users will be using this tool?"
- "Is this for temporary use or a long-term internal solution?"
- "Do you need high availability or is occasional downtime acceptable?"

*If Production Application:*

- Proceed to Phase 2 for detailed scale assessment

## Phase 2: Scale and Performance Requirements (Production Applications)

**User Base Questions:**

- "How many users do you expect when the application launches?"
- "What's your expected user growth over the next 12 months?"
- "Will usage be steady throughout the day or are there peak periods?"

**Geographic and Availability Questions:**

- "Will your users be primarily in one geographic region or globally distributed?"
- "What level of availability do you need? (e.g., 99.9%, 99.99%, or basic uptime)"
- "How critical is performance? Are sub-second response times required?"

**Scale Classification (Internal Use):**

- **Small Scale:** <10K users, single region, standard availability requirements
- **Medium Scale:** 10K-100K users, multi-region consideration, higher availability needs  
- **Large Scale:** >100K users, global distribution, enterprise-grade availability and performance

## Phase 3: Budget and Cost Considerations

**Budget Discovery Questions:**

- "Do you have specific budget constraints or cost targets for running this application?"
- "Is minimizing costs the top priority, or are you willing to invest more for better performance/reliability?"
- "Are you more concerned about predictable monthly costs or optimizing for the lowest possible spend?"

**Cost Preference Classification:**

- **Cost-Optimized:** Minimize expenses, accept some performance trade-offs, prefer consumption-based pricing
- **Balanced:** Reasonable cost with good performance, mix of reserved and consumption pricing
- **Performance-Focused:** Cost is secondary to performance and reliability, premium services acceptable

## Phase 4: Architectural and Stack Preferences

**Technology Approach Questions:**

- "Do you have experience with containers (Docker) or prefer to avoid container management?"
- "Are you interested in serverless approaches where you don't manage infrastructure?"
- "Do you need full control over the underlying infrastructure, or prefer managed services?"
- "Are there specific technologies or platforms you want to use or avoid?"

**Programming Language and Framework Preferences:**

- "What programming language does your team prefer or have the most experience with?" (JavaScript/TypeScript, Python, C#, Java, Go, or other)
- "Do you have preferences for specific frameworks?" (React, Angular, Vue for frontend; Express, FastAPI, Spring Boot for backend)
- "Are there any technology constraints from your organization or existing systems?"
- "Do you prefer strongly-typed languages or are you comfortable with dynamic typing?"

**Integration and Compliance Questions:**

- "Do you need to integrate with existing systems or databases?"
- "Are there any compliance requirements (HIPAA, SOC2, etc.) that influence your architecture choices?"
- "Do you have existing Azure services or subscriptions this should connect to?"

**Architectural Preference Classification:**

- **Containers:** Prefer containerized applications with orchestration (Azure Container Apps, AKS)
- **Serverless:** Prefer event-driven, fully managed compute (Azure Functions, Logic Apps)
- **Hybrid:** Mix of containers and serverless based on component needs
- **No Preference:** Open to recommendations based on best fit for requirements

**Technology Stack Preferences:**

- **Programming Language:** [JavaScript/TypeScript, Python, C#, Java, Go, Other]
- **Frontend Framework:** [React, Angular, Vue.js, No Preference, Other]
- **Backend Framework:** [Express/Node.js, FastAPI/Flask, ASP.NET Core, Spring Boot, No Preference, Other]
- **Database Preference:** [SQL-based, NoSQL, No Preference, Existing System Integration]

## Phase 5: Requirements Documentation

Create or update Application specification with a "Project Requirements" section containing:

```markdown
## Project Requirements

### Project Classification
- **Type:** [POC/Development Tool/Production Application]
- **Primary Purpose:** [Brief description]
- **Target Audience:** [Description of users]

### Scale Requirements
- **User Base:** [Expected users and growth]
- **Geographic Scope:** [Single region/Multi-region/Global]
- **Availability Needs:** [Uptime requirements]
- **Scale Category:** [Small/Medium/Large - for production apps]

### Budget Considerations
- **Cost Priority:** [Cost-Optimized/Balanced/Performance-Focused]
- **Budget Constraints:** [Any specific limitations mentioned]
- **Pricing Preference:** [Consumption vs Reserved vs Hybrid]

### Architectural Preferences
- **Stack Preference:** [Containers/Serverless/Hybrid/No Preference]
- **Programming Language:** [Preferred language and rationale]
- **Frontend Framework:** [Chosen framework if applicable]
- **Backend Framework:** [Chosen framework if applicable]
- **Infrastructure Control:** [Managed services vs Infrastructure control preference]
- **Integration Requirements:** [Existing systems to connect with]
- **Compliance Requirements:** [Any mentioned compliance needs]

### Key Insights
- [Any additional context that influences architecture decisions]
- [Special requirements or constraints mentioned]
```

**Conversation Flow Tips:**

- Start with open-ended questions and narrow down based on responses
- If user is unsure about technical terms, provide brief explanations
- For POCs, focus more on timeline and potential evolution rather than scale
- For production apps, spend more time on scale and reliability requirements
- Always ask "Is there anything else about your requirements I should know?" at the end
- Keep the tone consultative and helpful, not interrogative
