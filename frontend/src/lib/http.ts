import { API_BASE } from './env';

export class HttpError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = 'HttpError';
  }
}

export async function get<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`);
  if (!res.ok) {
    throw new HttpError(res.status, `GET ${path} failed: ${res.statusText}`);
  }
  return res.json();
}
