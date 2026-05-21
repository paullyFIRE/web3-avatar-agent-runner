import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { Providers } from '@/app/providers';
import { Nav } from '@/components/layout/nav';
import { JobsListPage } from '@/features/jobs/components/jobs-list';
import { JobDetailPage } from '@/features/jobs/components/job-detail-page';
import { HeartbeatsPage } from '@/features/agents/components/heartbeats-page';

const basename = window.location.pathname.startsWith('/ui') ? '/ui' : '/';

export function App() {
  return (
    <BrowserRouter basename={basename}>
      <Providers>
        <div className="min-h-screen bg-gray-50">
          <Nav />
          <main className="p-6 max-w-7xl mx-auto">
            <Routes>
              <Route path="/" element={<Navigate to="/jobs" replace />} />
              <Route path="/jobs" element={<JobsListPage />} />
              <Route path="/jobs/:id" element={<JobDetailPage />} />
              <Route path="/heartbeats" element={<HeartbeatsPage />} />
            </Routes>
          </main>
        </div>
      </Providers>
    </BrowserRouter>
  );
}
