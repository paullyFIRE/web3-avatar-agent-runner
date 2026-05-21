import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { fetchJobs, fetchStatus } from '../api';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import { fmtDate } from '@/lib/cn';
import { Activity, AlertCircle, CheckCircle, Clock, Play, Loader2 } from 'lucide-react';

function StatCard({
  label,
  value,
  icon,
  color,
}: {
  label: string;
  value: number;
  icon: React.ReactNode;
  color: string;
}) {
  return (
    <Card className="p-4 flex items-center gap-3">
      <div className={`${color}`}>{icon}</div>
      <div>
        <div className="text-sm text-gray-500">{label}</div>
        <div className="text-2xl font-bold">{value}</div>
      </div>
    </Card>
  );
}

export function JobsListPage() {
  const { data: status, isLoading: statusLoading } = useQuery({
    queryKey: ['status'],
    queryFn: fetchStatus,
  });
  const { data: jobs, isLoading: jobsLoading } = useQuery({
    queryKey: ['jobs'],
    queryFn: fetchJobs,
  });

  if (statusLoading || jobsLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-8 w-8 animate-spin text-gray-400" />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-3 md:grid-cols-6 gap-4">
        <StatCard label="Total" value={status?.total ?? 0} icon={<Activity className="h-5 w-5" />} color="text-gray-600" />
        <StatCard label="Running" value={status?.running ?? 0} icon={<Play className="h-5 w-5" />} color="text-purple-600" />
        <StatCard label="Queued" value={status?.queued ?? 0} icon={<Clock className="h-5 w-5" />} color="text-yellow-600" />
        <StatCard label="Failed" value={status?.failed ?? 0} icon={<AlertCircle className="h-5 w-5" />} color="text-red-600" />
        <StatCard label="Review" value={status?.waiting ?? 0} icon={<CheckCircle className="h-5 w-5" />} color="text-blue-600" />
        <StatCard label="Retry" value={status?.retry ?? 0} icon={<Loader2 className="h-5 w-5" />} color="text-orange-600" />
      </div>

      <Card>
        <CardHeader>Jobs</CardHeader>
        <CardContent className="p-0">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-gray-600">
              <tr>
                <th className="text-left px-4 py-2">ID</th>
                <th className="text-left px-4 py-2">Issue</th>
                <th className="text-left px-4 py-2">State</th>
                <th className="text-left px-4 py-2">Attempt</th>
                <th className="text-left px-4 py-2">Created</th>
                <th className="text-left px-4 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {jobs?.map((job) => (
                <tr key={job.id} className="border-t hover:bg-gray-50">
                  <td className="px-4 py-2 font-mono">{job.id}</td>
                  <td className="px-4 py-2">
                    <a
                      href={`https://github.com/paullyFIRE/web3-avatar/issues/${job.issue_number}`}
                      className="text-blue-600 hover:underline"
                    >
                      #{job.issue_number}
                    </a>
                    {job.pr_number && (
                      <>
                        {' → '}
                        <a
                          href={`https://github.com/paullyFIRE/web3-avatar/pull/${job.pr_number}`}
                          className="text-blue-600 hover:underline"
                        >
                          PR #{job.pr_number}
                        </a>
                      </>
                    )}
                  </td>
                  <td className="px-4 py-2"><Badge state={job.state} /></td>
                  <td className="px-4 py-2">{job.attempt}/{job.max_attempts}</td>
                  <td className="px-4 py-2 text-gray-500">{fmtDate(job.created_at)}</td>
                  <td className="px-4 py-2">
                    <Link to={`/jobs/${job.id}`} className="text-blue-600 hover:underline text-xs">
                      View
                    </Link>
                  </td>
                </tr>
              ))}
              {(!jobs || jobs.length === 0) && (
                <tr>
                  <td colSpan={6} className="px-4 py-8 text-center text-gray-500">
                    No jobs yet
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </CardContent>
      </Card>
    </div>
  );
}
