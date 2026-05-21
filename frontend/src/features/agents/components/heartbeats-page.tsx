import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { fetchAllJobs } from '../api';
import { fetchJob } from '@/features/jobs/api';
import { Badge } from '@/components/ui/badge';
import { Card } from '@/components/ui/card';
import { Loader2, Activity } from 'lucide-react';
import { cn } from '@/lib/cn';

export function HeartbeatsPage() {
  const { data: jobs, isLoading } = useQuery({
    queryKey: ['jobs'],
    queryFn: fetchAllJobs,
  });

  const [selectedId, setSelectedId] = useState<number | null>(null);
  const { data: selectedJob } = useQuery({
    queryKey: ['job', selectedId],
    queryFn: () => fetchJob(selectedId!),
    enabled: !!selectedId,
  });

  const [log, setLog] = useState('Select a job to view logs');
  const handleSelect = async (id: number) => {
    setSelectedId(id);
    try {
      const res = await fetch(`/jobs/${id}/logs`);
      setLog(await res.text());
    } catch {
      setLog('No logs available.');
    }
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
      </div>
    );
  }

  return (
    <div className="flex gap-4 h-[calc(100vh-8rem)]">
      <div className="w-72 shrink-0 overflow-y-auto space-y-1">
        {jobs?.map((job) => (
          <button
            key={job.id}
            onClick={() => handleSelect(job.id)}
            className={cn(
              'w-full text-left px-3 py-2 rounded-md text-sm hover:bg-gray-100 transition-colors',
              selectedId === job.id && 'bg-blue-50 border-l-2 border-l-blue-500',
            )}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="font-mono">#{job.issue_number}</span>
              <Badge state={job.state} />
            </div>
            <div className="flex items-center gap-2 mt-1">
              <Activity className="h-3 w-3 text-gray-400" />
              <span className="text-xs text-gray-500">
                {job.heartbeat_at
                  ? new Date(job.heartbeat_at).toLocaleString()
                  : '-'}
              </span>
            </div>
            <div className="text-xs text-gray-400 mt-0.5">
              attempt {job.attempt}/{job.max_attempts} · PID {job.pid ?? '-'}
            </div>
          </button>
        ))}
      </div>

      <div className="flex-1">
        <Card className="h-full flex flex-col">
          {selectedJob ? (
            <>
              <div className="px-4 py-3 border-b bg-gray-50 flex items-center justify-between shrink-0">
                <div className="flex items-center gap-2">
                  <span className="font-semibold text-sm">
                    Job #{selectedJob.id} · Issue #{selectedJob.issue_number}
                  </span>
                  <Badge state={selectedJob.state} />
                </div>
                <div className="text-xs text-gray-500">
                  {selectedJob.heartbeat_at && (
                    <>Last heartbeat: {new Date(selectedJob.heartbeat_at).toLocaleString()}</>
                  )}
                  {selectedJob.pid && <> · PID {selectedJob.pid}</>}
                </div>
              </div>
              <pre className="text-xs font-mono whitespace-pre-wrap overflow-y-auto p-3 flex-1 bg-gray-950 text-green-400">
                {log}
              </pre>
            </>
          ) : (
            <div className="flex items-center justify-center h-full text-gray-400 text-sm">
              Select a job to view heartbeat details
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
