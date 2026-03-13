import { parseRepoFullName } from "./inputs";

describe("parseRepoFullName", () => {
  it("parses valid owner/repo format", () => {
    const [owner, repo] = parseRepoFullName("Azure/azure-dev");
    expect(owner).toBe("Azure");
    expect(repo).toBe("azure-dev");
  });

  it("parses repo names with dots and hyphens", () => {
    const [owner, repo] = parseRepoFullName("MicrosoftDocs/azure-dev-docs-pr");
    expect(owner).toBe("MicrosoftDocs");
    expect(repo).toBe("azure-dev-docs-pr");
  });

  it("throws on single segment", () => {
    expect(() => parseRepoFullName("noslash")).toThrow('Invalid repository format');
  });

  it("throws on empty string", () => {
    expect(() => parseRepoFullName("")).toThrow('Invalid repository format');
  });

  it("throws on too many segments", () => {
    expect(() => parseRepoFullName("a/b/c")).toThrow('Invalid repository format');
  });

  it("throws on empty owner", () => {
    expect(() => parseRepoFullName("/repo")).toThrow('Invalid repository format');
  });

  it("throws on repo name with spaces", () => {
    expect(() => parseRepoFullName("evil owner/repo")).toThrow('Owner and repo must contain only');
  });

  it("throws on repo name with special characters", () => {
    expect(() => parseRepoFullName("owner/repo;rm -rf")).toThrow('Owner and repo must contain only');
  });
});
