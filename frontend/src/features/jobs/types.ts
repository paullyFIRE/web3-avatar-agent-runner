export interface Job {
  id: number;
  repo_owner: string;
  repo_name: string;
  issue_number: number;
  pr_number: number | null;
  branch: string | null;
  worktree_path: string | null;
  job_type: string;
  state: string;
  current_phase: string | null;
  attempt: number;
  max_attempts: number;
  next_retry_at: string | null;
  pid: number | null;
  heartbeat_at: string | null;
  last_log_line: string | null;
  last_error: string | null;
  model: string | null;
  trigger_comment_id: string | null;
  created_at: string;
  updated_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface StateLog {
  id: number;
  job_id: number;
  from_state: string | null;
  to_state: string;
  message: string | null;
  created_at: string;
}

export interface Status {
  total: number;
  running: number;
  queued: number;
  failed: number;
  waiting: number;
  retry: number;
}
