import { get } from '@/lib/http';
import type { Job } from '@/features/jobs/types';

export async function fetchAllJobs(): Promise<Job[]> {
  return get<Job[]>('/jobs');
}
