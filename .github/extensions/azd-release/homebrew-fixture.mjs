// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

export function homebrewFixture(cask, version = "1.2.3") {
    const url = (name) =>
        `url "https://github.com/Azure/azure-dev/releases/download/azure-dev-cli_${cask ? "#{version}" : version}/azd-${name}"`;
    const hash = `"${"a".repeat(64)}"`;
    return (cask ? [
        'cask "azd" do', 'arch arm: "arm64", intel: "amd64"', `version "${version}"`,
        `sha256 arm: ${hash},`, `intel: ${hash},`, `arm64_linux: ${hash},`, `x86_64_linux: ${hash}`,
        "on_macos do", url("darwin-#{arch}.zip"), 'binary "azd-darwin-#{arch}", target: "azd"', "end",
        "on_linux do", url("linux-#{arch}.tar.gz"), 'binary "azd-linux-#{arch}", target: "azd"', "end", "end",
    ] : [
        "class Azd < Formula",
        "if OS.mac?",
        "if Hardware::CPU.intel?", url("darwin-amd64.zip"), `sha256 ${hash}`,
        "elsif Hardware::CPU.arm?", url("darwin-arm64.zip"), `sha256 ${hash}`, "end",
        "elsif OS.linux?",
        "if Hardware::CPU.intel?", url("linux-amd64.tar.gz"), `sha256 ${hash}`,
        "elsif Hardware::CPU.arm?", url("linux-arm64.tar.gz"), `sha256 ${hash}`, "end",
        "end", `version "${version}"`, "end",
    ]).join("\n");
}
