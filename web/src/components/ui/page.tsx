import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';

export function PageHeader(props: {
  title: ReactNode;
  desc?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('mb-6 flex flex-wrap items-end justify-between gap-3', props.className)}>
      <div>
        <h1 className="text-xl font-semibold tracking-tight text-[var(--ltzy-fg)]">{props.title}</h1>
        {props.desc ? (
          <p className="mt-1 text-sm text-[var(--ltzy-muted)]">{props.desc}</p>
        ) : null}
      </div>
      {props.actions ? <div className="flex flex-wrap items-center gap-2">{props.actions}</div> : null}
    </div>
  );
}

export function StatCard(props: {
  label: ReactNode;
  value: ReactNode;
  hint?: ReactNode;
  icon?: ReactNode;
  tone?: 'default' | 'success' | 'warning' | 'danger' | 'primary';
}) {
  const toneMap: Record<string, string> = {
    default: 'text-[var(--ltzy-fg)]',
    success: 'text-[var(--ltzy-success)]',
    warning: 'text-[var(--ltzy-warning)]',
    danger: 'text-[var(--ltzy-danger)]',
    primary: 'text-[var(--ltzy-primary)]',
  };
  return (
    <div className="rounded-[14px] border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)] p-5 transition-colors hover:border-[var(--ltzy-primary)]/40">
      <div className="flex items-start justify-between">
        <span className="text-xs font-medium text-[var(--ltzy-muted)]">{props.label}</span>
        {props.icon ? <span className="text-[var(--ltzy-muted)]">{props.icon}</span> : null}
      </div>
      <div
        className={cn(
          'mt-2 font-mono text-2xl font-semibold tracking-tight',
          toneMap[props.tone ?? 'default'],
        )}
      >
        {props.value}
      </div>
      {props.hint ? (
        <div className="mt-1 text-xs text-[var(--ltzy-muted)]">{props.hint}</div>
      ) : null}
    </div>
  );
}
