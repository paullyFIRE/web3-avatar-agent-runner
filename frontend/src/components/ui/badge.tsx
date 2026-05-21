import { cn } from '@/lib/cn';

const variantClasses: Record<string, string> = {
  queued: 'bg-yellow-100 text-yellow-800',
  retry_scheduled: 'bg-yellow-100 text-yellow-800',
  waiting_for_review: 'bg-blue-100 text-blue-800',
  failed: 'bg-red-100 text-red-800',
  blocked: 'bg-red-100 text-red-800',
  closed_without_merge: 'bg-red-100 text-red-800',
  merged: 'bg-green-100 text-green-800',
  cleanup_done: 'bg-green-100 text-green-800',
  running_agent: 'bg-purple-100 text-purple-800',
  preparing_worktree: 'bg-purple-100 text-purple-800',
  needs_clarification: 'bg-orange-100 text-orange-800',
};

export function Badge({ state }: { state: string }) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded px-2 py-0.5 text-xs font-medium',
        variantClasses[state] ?? 'bg-gray-100 text-gray-800',
      )}
    >
      {state}
    </span>
  );
}
