import { get } from '@/lib/http';
import type { Job, StateLog, Status } from './types';

export async function fetchStatus(): Promise<Status> {
  return get<Status>('/status');
}

export async function fetchJobs(): Promise<Job[]> {
  return get<Job[]>('/jobs');
}

export async function fetchJob(id: number): Promise<Job> {
  return get<Job>(`/jobs/${id}`);
}

export async function fetchJobStates(id: number): Promise<StateLog[]> {
  return get<StateLog[]>(`/jobs/${id}/states`);
}
