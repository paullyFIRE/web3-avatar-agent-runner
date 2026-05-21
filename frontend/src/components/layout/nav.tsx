import { Link, useLocation } from 'react-router-dom';
import { cn } from '@/lib/cn';

const links = [
  { to: '/jobs', label: 'Jobs' },
  { to: '/heartbeats', label: 'Heartbeats' },
];

export function Nav() {
  const { pathname } = useLocation();
  return (
    <nav className="bg-gray-900 text-white px-6 py-3 flex items-center gap-6">
      <Link to="/" className="font-bold text-lg">
        Agent Runner
      </Link>
      {links.map((link) => (
        <Link
          key={link.to}
          to={link.to}
          className={cn(
            'hover:text-gray-300 transition-colors',
            pathname.startsWith(link.to) && 'text-gray-300',
          )}
        >
          {link.label}
        </Link>
      ))}
    </nav>
  );
}
