# External Prompting

## Problem

During operations, `azd` may need to prompt the user for information. For example, during `init` for an Aspire application, we prompt the user to select which services should be exposed to the Internet. The first time `azd provision` is run for an environment, we ask the user to select an subscription and location. In addition, the IaC we provision for an application may have parameters which `azd` needs to prompt the user for.

Today, this prompting happens via the terminal - we have a set of methods that can be called on `input.Console` that allow different types of prompts:

- `Prompt`: Asks the user for a single (freeform) text value.
- `Select`: Asks the user to pick a single value from a list of options.
- `MultiSelect`: Asks the user to pick zero or more values from a list of options.
- `Confirm`: Asks the user to confirm and operation with a given message.

The implementation of this interface uses a go library to provide a terminal experience (using ANSI escape sequences to provide a nice terminal interaction model) with a fallback to raw text input when the user is not connected to a proper terminal.

This is a reasonable experience for users interacting with `azd` via their terminal.  However, `azd` also supports being used within IDEs (today Visual Studio Code, tomorrow Visual Studio as well) and there our terminal based prompting strategy is not ideal. VS Code is forced to run us in an interactive terminal and the user has to interact with `azd` via its terminal interface or specifically craft their calls of `azd` to not prompt the user.  In Visual Studio, AZD is run in a background process, so no terminal interaction is possible.

In both cases it would be ideal if `azd` could delegate the prompting behavior back to the caller.  This document outlines a solution, which provides a way for an external tool to provide a remote service that `azd` interacts with when it needs to prompt the user for information.

## Solution

Similar to our strategy for delegating authentication to an external host, we support delegating prompting to an external host via a special JSON based REST API, hosted over a local HTTP server.  When run, `azd` looks for the following environment variables:

- `AZD_UI_PROMPT_ENDPOINT` (required)
- `AZD_UI_PROMPT_KEY` (required)
- `AZD_UI_NO_PROMPT_DIALOG` (optional)

When both `AZD_UI_PROMPT_ENDPOINT` and `AZD_UI_PROMPT_KEY` are set, instead of prompting using the command line - the implementation of our prompting methods now make a POST call to a special endpoint:

`${AZD_UI_PROMPT_ENDPOINT}/prompt?api-version=2024-02-14-preview`

Setting the following headers:

- `Content-Type: application/json`
- `Authorization: Bearer ${AZD_UI_PROMPT_KEY}`

The use of `AZD_UI_PROMPT_KEY` allows the host to block requests coming from other clients on the same machine (since the it is expected the host runs a ephemeral HTTP server listing on `127.0.0.1` on a random port). It is expected that the host will generate a random string and use this as a shared key for the lifetime of an `azd` invocation.

## Simple Prompt API

The body of the request contains a JSON object with all the information about the prompt that `azd` needs a response for:

```typescript
interface PromptRequest {
    type: "string" | "password" | "directory" | "select" | "multiSelect" | "confirm"
    options: {
        message: string // the message to be displayed as part of the prompt
        help?: string // optional help text that can be displayed upon request
        choices?: PromptChoice[]
        defaultValue?: string | string[] | boolean
    }
}

interface PromptChoice {
    value: string
    detail?: string
}
```

The `password` type represents a string value which represents a password. The host may want to use a different UI element (perhaps one that uses `***` instead of characters) when prompting.

The server should respond with 200 OK and the body that represents the result:

```typescript
interface PromptResponse {
    status: "success" | "cancelled" | "error"
    
    // present when status is "success"
    value?: string | string[]

    // present when status is "error"
    message?: string 
}
```

### Success 

When the host is able to prompt for the value a response with the `status` of `success` is sent.

When the type is `confirm` the value should be either `"true"` or `"false"` (a string, not a JSON boolean) indicating if the user confirmed the operation (`"true"`) or rejected it (`"false"`). Note that a user rejecting a confirm prompt still results in a `"success"` status (the value is simply `"false"`). In the case of `multiSelect` an array of string values is returned, each individual value is a value from the `choices` array that was selected by the user.

### Cancelled

The user may decline to provide a response (imagine hitting a cancel button on the dialog that is being used to present the question, or a user hitting something like CTRL+C in a terminal interaction to abort the question asking but not the entire application).

In this case the `status` is `cancelled`.

`azd` returns a special `Error` type internally in this case, which up-stack code can use.

### Error

Some error happened during prompting - the status is `error` and the `message` property is a human readable error message that `azd` returns as a go `error`.

Note that an error prompting leads to a successful result at the HTTP layer (200 OK) but with a special error object. `azd` treats other responses as if the server has an internal bug.

## Prompt Dialog API

In addition to the simple prompt API, `azd` supports a **Prompt Dialog API** that allows collecting multiple parameter values in a single request. This is primarily used by Visual Studio to present all required parameters in a single dialog.

When external prompting is enabled, `azd` checks `console.SupportsPromptDialog()` to determine whether to use the dialog API. By default, this returns `true` when external prompting is configured.

### Dialog Request Format

```typescript
interface PromptDialogRequest {
    title: string
    description: string
    prompts: PromptDialogPrompt[]
}

interface PromptDialogPrompt {
    id: string           // unique identifier for this prompt
    kind: "string" | "password" | "select" | "multiSelect" | "confirm"
    displayName: string
    description?: string
    defaultValue?: string
    required: boolean
    choices?: PromptDialogChoice[]
}

interface PromptDialogChoice {
    value: string
    description?: string
}
```

### Dialog Response Format

```typescript
interface PromptDialogResponse {
    result: "success" | "cancelled" | "error"
    message?: string  // present when result is "error"
    inputs?: PromptDialogResponseInput[]  // present when result is "success"
}

interface PromptDialogResponseInput {
    id: string    // matches the id from the request
    value: any    // the user-provided value
}
```

### How Visual Studio Uses Prompt Dialog

Visual Studio uses the Prompt Dialog API to collect all deployment parameters at once. When a user runs `azd provision`, VS:

1. Receives a single dialog request with all required parameters (location, resource names, etc.)
2. Presents a unified form/wizard UI to collect all values
3. Returns all values in a single response

This provides a better UX for GUI environments where presenting multiple sequential prompts would be jarring.

### Trade-offs of Prompt Dialog

**Advantages:**
- Single unified UI for collecting multiple values
- Better UX for GUI-based hosts like Visual Studio
- Host can present all parameters at once with custom validation

**Disadvantages:**
- **Location prompts lose their options**: When using the dialog API, location parameters are sent as simple `kind: "string"` prompts without the list of available Azure locations. The host must either:
  - Require users to type location names manually (e.g., `eastus`, `westus2`)
  - Fetch and populate the location list independently
- **No rich metadata**: Parameters with special `azd.type` metadata (like `location`) are converted to basic string inputs
- **Host complexity**: The host must handle the dialog format and potentially fetch additional data

### Disabling Prompt Dialog

For hosts that want external prompting but prefer individual prompts with full options (including location lists), set:

```
AZD_UI_NO_PROMPT_DIALOG=1
```

When this environment variable is set (to any non-empty value), `azd` will:
- Still use external prompting for all user interactions
- Send each prompt individually using the Simple Prompt API
- Include full choice lists for location and other select prompts

This is the recommended approach for terminal-based UIs or custom TUIs that can handle sequential prompts.

## Example Implementation: ConcurX Extension

The `microsoft.azd.concurx` extension demonstrates how to implement external prompting in a Bubble Tea TUI. Here's the key implementation pattern:

### 1. Create a Prompt Server

```go
// prompt_server.go - HTTP server to receive prompts from azd subprocesses

type PromptServer struct {
    listener   net.Listener
    server     *http.Server
    endpoint   string
    key        string
    ui         *tea.Program  // Bubble Tea program to send prompts to
    pendingReq *pendingPrompt
}

func NewPromptServer(ctx context.Context) (*PromptServer, error) {
    // Generate random key for authentication
    keyBytes := make([]byte, 32)
    rand.Read(keyBytes)
    key := hex.EncodeToString(keyBytes)

    // Create listener on random port
    listener, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        return nil, err
    }

    endpoint := fmt.Sprintf("http://%s", listener.Addr().String())
    
    ps := &PromptServer{
        listener: listener,
        endpoint: endpoint,
        key:      key,
    }

    mux := http.NewServeMux()
    mux.HandleFunc("/prompt", ps.handlePrompt)
    ps.server = &http.Server{Handler: mux}

    return ps, nil
}

// EnvVars returns environment variables to pass to azd subprocesses
func (ps *PromptServer) EnvVars() []string {
    return []string{
        fmt.Sprintf("AZD_UI_PROMPT_ENDPOINT=%s", ps.endpoint),
        fmt.Sprintf("AZD_UI_PROMPT_KEY=%s", ps.key),
        // Disable dialog to get individual prompts with full location list
        "AZD_UI_NO_PROMPT_DIALOG=1",
    }
}

func (ps *PromptServer) handlePrompt(w http.ResponseWriter, r *http.Request) {
    // Validate auth header
    if r.Header.Get("Authorization") != fmt.Sprintf("Bearer %s", ps.key) {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }

    // Parse request
    var req PromptRequest
    json.NewDecoder(r.Body).Decode(&req)

    // Create response channel and send to TUI
    responseChan := make(chan *PromptResponse, 1)
    ps.pendingReq = &pendingPrompt{request: &req, response: responseChan}
    
    // Send to Bubble Tea program
    ps.ui.Send(promptRequestMsg{request: &req})

    // Wait for response from TUI
    response := <-responseChan
    
    // Send response back to azd
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}
```

### 2. Pass Environment Variables to Subprocesses

```go
// concurrent_deployer.go - Running azd commands with prompt support

func (d *ConcurrentDeployer) runProvision(ctx context.Context, project *DeploymentProject) error {
    args := []string{"provision", "--cwd", project.Path}
    
    cmd := exec.CommandContext(ctx, "azd", args...)
    
    // Pass prompt server env vars so azd uses external prompting
    cmd.Env = append(os.Environ(), d.promptServer.EnvVars()...)
    
    return cmd.Run()
}
```

### 3. Handle Prompts in the TUI

```go
// deployment_model.go - Bubble Tea model handling prompts

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case promptRequestMsg:
        // Switch to prompt view
        m.viewMode = viewPrompt
        m.promptModel = newPromptModel(msg.request)
        return m, nil
        
    case tea.KeyMsg:
        if m.viewMode == viewPrompt {
            // Handle prompt input
            m.promptModel, cmd = m.promptModel.Update(msg)
            
            if m.promptModel.submitted {
                // Send response back to prompt server
                response := m.promptModel.GetResponse()
                m.promptServer.RespondToPrompt(response)
                m.viewMode = viewDeployment
            }
            return m, cmd
        }
    }
    return m, nil
}
```

### 4. Render Prompt UI

```go
// prompt_model.go - UI for displaying prompts

func (m promptModel) View() string {
    var content strings.Builder
    
    content.WriteString(m.message)
    content.WriteString("\n")
    
    switch m.promptType {
    case PromptTypeSelect:
        // Show filterable list with scroll support
        for i := m.scrollOffset; i < min(m.scrollOffset+maxVisible, len(m.filteredChoices)); i++ {
            choice := m.choices[m.filteredIndices[i]]
            if i == m.selectedIndex {
                content.WriteString("▸ " + choice.Value)
            } else {
                content.WriteString("  " + choice.Value)
            }
            content.WriteString("\n")
        }
        content.WriteString("Type to filter • ↑/↓ navigate • Enter select")
        
    case PromptTypeString:
        content.WriteString(m.textInput.View())
        content.WriteString("\nEnter to submit • Esc to cancel")
    }
    
    return content.String()
}
```

This pattern allows the extension to:
- Run `azd provision` and `azd deploy` as subprocesses
- Intercept all prompts and display them in a custom TUI
- Get full location lists by using `AZD_UI_NO_PROMPT_DIALOG=1`
- Provide filtering, scrolling, and keyboard navigation for long lists

## Open Issues

- [x] ~~Some hosts, such as VS, may want to collect a set of prompts up front and present them all on a single page as part of an end to end - how would we support this?~~ **Resolved**: The Prompt Dialog API supports this use case.
- [ ] Consider adding a way for the dialog API to request location lists from azd, so hosts using dialog mode don't have to fetch locations independently.
