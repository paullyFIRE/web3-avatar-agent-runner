import { useQuery } from '@tanstack/react-query';
import { useParams, Link } from 'react-router-dom';
import { fetchJob, fetchJobStates } from '../api';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import { fmtDate } from '@/lib/cn';
import { ChevronLeft, Loader2 } from 'lucide-react';
import { useEffect, useState } from 'react';

export function JobDetailPage() {
  const { id } = useParams<{ id: string }>();
  const jobId = Number(id);

  const { data: job, isLoading } = useQuery({
    queryKey: ['job', jobId],
    queryFn: () => fetchJob(jobId),
    enabled: !!jobId,
  });

  const { data: states } = useQuery({
    queryKey: ['job-states', jobId],
    queryFn: () => fetchJobStates(jobId),
    enabled: !!jobId,
  });

  const [log, setLog] = useState('Loading logs...');
  useEffect(() => {
    const load = async () => {
      try {
        const res = await fetch(`/api/jobs/${jobId}/logs`);
        const text = await res.text();
        if (text) setLog(text);
      } catch { /* ignore */ }
    };
    load();
    const interval = setInterval(load, 3000);
    return () => clearInterval(interval);
  }, [jobId]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
      </div>
    );
  }

  if (!job) {
    return <div className="text-center py-12 text-gray-500">Job not found</div>;
  }

  return (
    <div className="space-y-4">
      <Link to="/jobs" className="inline-flex items-center gap-1 text-sm text-blue-600 hover:underline">
        <ChevronLeft className="h-4 w-4" /> Back to Jobs
      </Link>

      <div className="flex gap-4">
        <div className="flex-1 space-y-4">
          <Card>
            <CardContent className="p-4">
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
                <div><div className="text-gray-500">ID</div><div className="font-mono">{job.id}</div></div>
                <div><div className="text-gray-500">Issue</div><div className="font-mono">#{job.issue_number}</div></div>
                <div><div className="text-gray-500">PR</div><div className="font-mono">{job.pr_number ? `#${job.pr_number}` : '-'}</div></div>
                <div><div className="text-gray-500">State</div><div><Badge state={job.state} /></div></div>
                <div><div className="text-gray-500">Attempt</div><div>{job.attempt}/{job.max_attempts}</div></div>
                <div><div className="text-gray-500">Created</div><div className="text-xs">{fmtDate(job.created_at)}</div></div>
                {job.branch && <div><div className="text-gray-500">Branch</div><div className="font-mono text-xs">{job.branch}</div></div>}
                {job.pid && <div><div className="text-gray-500">PID</div><div className="font-mono">{job.pid}</div></div>}
                {job.heartbeat_at && <div><div className="text-gray-500">Heartbeat</div><div className="text-xs">{fmtDate(job.heartbeat_at)}</div></div>}
              </div>
              {job.last_error && (
                <div className="mt-4 p-3 bg-red-50 border border-red-200 rounded text-sm text-red-700">
                  <strong>Error:</strong> {job.last_error}
                </div>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardContent className="p-0">
              <div className="px-4 py-2 border-b text-sm font-medium text-gray-700">Agent Logs</div>
              <pre className="text-xs font-mono whitespace-pre-wrap h-96 overflow-y-auto p-3 bg-gray-950 text-green-400">
                {log}
              </pre>
            </CardContent>
          </Card>
        </div>

        <div className="w-80 shrink-0">
          <Card>
            <CardContent className="p-3">
              <h3 className="text-sm font-semibold mb-3">State Timeline</h3>
              <div className="space-y-0 overflow-y-auto max-h-[calc(100vh-16rem)]">
                {states?.slice().reverse().map((s) => (
                  <div key={s.id} className="flex gap-2 py-1.5 border-l-2 border-gray-300 pl-3">
                    <div className="text-xs">
                      <div className="flex items-center gap-1 flex-wrap">
                        {s.from_state && (
                          <span className="text-xs font-mono bg-gray-100 px-1 rounded">{s.from_state}</span>
                        )}
                        <span className="text-gray-400">→</span>
                        <span className="text-xs font-mono bg-gray-100 px-1 rounded">{s.to_state}</span>
                      </div>
                      {s.message && <div className="text-gray-600 mt-0.5 text-xs">{s.message}</div>}
                      <div className="text-gray-400 mt-0.5 text-xs">{fmtDate(s.created_at)}</div>
                    </div>
                  </div>
                ))}
                {(!states || states.length === 0) && (
                  <div className="text-sm text-gray-500 py-4 text-center">No state changes</div>
                )}
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
