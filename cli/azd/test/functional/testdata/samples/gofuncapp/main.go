// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"fmt"
	"net/http"

	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/worker"
)

func main() {
	app := sdk.FunctionApp()
	app.HTTP("hello", hello,
		sdk.WithMethods("GET", "POST"),
		sdk.WithAuth("anonymous"),
	)
	worker.Start(app)
}

func hello(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "Azure"
	}
	fmt.Fprintf(w, "Hello, %s! Welcome to Go on Azure Functions.", name)
}
