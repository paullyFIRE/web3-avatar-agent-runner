import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';

export function Card({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn('rounded-lg border bg-white shadow-sm', className)}>
      {children}
    </div>
  );
}

export function CardHeader({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn('border-b px-4 py-3 font-medium', className)}>
      {children}
    </div>
  );
}

export function CardContent({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return <div className={cn('p-4', className)}>{children}</div>;
}
