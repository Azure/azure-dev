/** Shared type definitions for doc-monitor action. */

export interface PrInfo {
  number: number;
  title: string;
  body: string | null;
  baseBranch: string;
  headBranch: string;
  state: string;
  merged: boolean;
  htmlUrl: string;
}

export interface FileDiff {
  path: string;
  status: "added" | "modified" | "deleted" | "renamed";
  previousPath?: string;
  additions: number;
  deletions: number;
  patch?: string;
}

export type ChangeCategory =
  | "api"
  | "behavior"
  | "config"
  | "feature"
  | "deprecation"
  | "bugfix"
  | "docs"
  | "test"
  | "infra"
  | "other";

export interface ClassifiedChange {
  files: FileDiff[];
  category: ChangeCategory;
  summary: string;
}

export interface DocEntry {
  repo: string;
  path: string;
  title: string;
  topics: string[];
}

export interface DocImpact {
  doc: DocEntry;
  action: "create" | "update" | "delete";
  reason: string;
  suggestedChanges?: string;
  priority: "high" | "medium" | "low";
}

export interface AnalysisResult {
  impacts: DocImpact[];
  summary: string;
  noImpact: boolean;
}

export interface CompanionPr {
  repo: string;
  number: number;
  branch: string;
  htmlUrl: string;
  status: "created" | "updated" | "existing" | "conflict" | "error";
  message?: string;
}

export interface TrackingState {
  sourcePr: number;
  lastUpdated: string;
  inRepoPr?: CompanionPr;
  externalPr?: CompanionPr;
  analysisResult: AnalysisResult;
}

export interface ActionInputs {
  githubToken: string;
  docsRepoToken: string;
  mode: "auto" | "single" | "all_open" | "list";
  prNumber?: number;
  prList?: number[];
  docsAssignees: string[];
  sourceRepo: string;
  docsRepo: string;
}
