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

Similar to our strategy for delegating authentication to an external host, we support delegating prompting to an external host via a special JSON based REST API, hosted over a local HTTP server.  When run, `azd` looks for two special environment variables:

- `AZD_UI_PROMPT_ENDPOINT`
- `AZD_UI_PROMPT_KEY`

When both are set, instead of prompting using the command line - the implementation of our prompting methods now make a POST call to a special endpoint:

`${AZD_UI_PROMPT_ENDPOINT}/prompt?api-version=2024-02-14-preview`

Setting the following headers:

- `Content-Type: application/json`
- `Authorization: Bearer ${AZD_UI_PROMPT_KEY}`

The use of `AZD_UI_PROMPT_KEY` allows the host to block requests coming from other clients on the same machine (since the it is expected the host runs a ephemeral HTTP server listing on `127.0.0.1` on a random port). It is expected that the host will generate a random string and use this as a shared key for the lifetime of an `azd` invocation.

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

## Open Issues

- [ ] Some hosts, such as VS, may want to collect a set of prompts up front and present them all on a single page as part of an end to end - how would we support this? It may be that the answer is "that's a separate API" and this solution is simply focused on "when `azd` it self is driving and end to end workflow".
