/**
 * API types and data transfer models for Garagefab.
 * Traceable to spec.md data contracts (§6) and API specifications (§7).
 */

export type WorkType = 'feature' | 'refactor' | 'bug_fix' | 'docs';

export type JobStatus =
  | 'queued'
  | 'running'
  | 'needs_clarification'
  | 'spec_review'
  | 'awaiting_approval'
  | 'done'
  | 'failed'
  | 'interrupted'
  | 'cancelled';

export type JobStage =
  | '01_Intent'
  | '02_Clarification_and_Spec'
  | '04_Coding'
  | '05_Independent_Review'
  | '06_Human_Approval_Gate'
  | '07_Done';

export interface Project {
  id: number;
  name: string;
  repo_path: string;
  base_ref: string;
  enabled_work_types: WorkType[];
  is_archived: number;
  created_at: string;
  updated_at: string;
}

export interface Job {
  id: number;
  project_id: number;
  project_name?: string;
  work_type: WorkType;
  title: string;
  intent: string;
  source: string;
  source_ref: string;
  stage: string;
  status: JobStatus;
  branch_name: string;
  worktree_path: string;
  base_sha: string;
  head_sha: string;
  pr_url: string;
  created_at: string;
  updated_at: string;
}

export interface StepRun {
  id: number;
  job_id: number;
  stage: string;
  kind: string;
  attempt: number;
  executor: string;
  status: 'running' | 'completed' | 'failed';
  failure_category?: string;
  exit_code?: number;
  log_path?: string;
  started_at: string;
  ended_at?: string;
}

export interface SystemEvent {
  id: number;
  job_id: number;
  job_title?: string;
  type: string;
  payload: string;
  created_at: string;
}

export interface AttentionItem {
  id: number;
  project_id: number;
  project_name: string;
  work_type: WorkType;
  title: string;
  stage: string;
  status: JobStatus;
  updated_at: string;
}

export interface OverviewData {
  job_counts: Record<JobStatus, number>;
  attention_list: AttentionItem[];
  recent_activity: SystemEvent[];
  orphan_worktrees: string[];
  intake_errors: string[];
}

export interface ClarificationAnswer {
  q: number;
  answer: string;
}

export interface ClarificationQuestionItem {
  q: number;
  question: string;
  context?: string;
}

export interface EvidenceSummary {
  verdict: 'PASSED' | 'FLAWED' | 'BLOCKED';
  checks: {
    build: boolean;
    test: boolean;
    lint: boolean;
  };
  tests: {
    passed: number;
    failed: number;
    skipped: number;
  };
  diff_stats: {
    files_changed: number;
    insertions: number;
    deletions: number;
  };
  warnings?: string[];
  drill_down: {
    diff_url: string;
    review_report_url?: string;
  };
}

export interface ApiError {
  error: {
    code: string;
    message: string;
    details?: Record<string, string>;
  };
}
