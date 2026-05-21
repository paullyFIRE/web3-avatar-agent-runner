import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function fmtDate(s: string | null | undefined): string {
  if (!s) return '-';
  if (s.startsWith('0001') || s.startsWith('1/1/1')) return '-';
  const d = new Date(s);
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return '-';
  return d.toLocaleString();
}
