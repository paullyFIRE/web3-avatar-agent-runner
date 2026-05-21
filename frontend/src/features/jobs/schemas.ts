import { z } from 'zod';

export const JobSchema = z.object({
  id: z.number(),
  issue_number: z.number(),
  pr_number: z.number().nullable(),
  state: z.string(),
  attempt: z.number(),
  max_attempts: z.number(),
  branch: z.string().nullable(),
  heartbeat_at: z.string().nullable(),
  pid: z.number().nullable(),
  last_error: z.string().nullable(),
  created_at: z.string(),
  started_at: z.string().nullable(),
  finished_at: z.string().nullable(),
});

export const JobsArraySchema = z.array(JobSchema);
