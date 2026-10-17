import type { ReactNode } from 'react';
import { X } from 'lucide-react';
import { cn } from '@/lib/cn';

interface CardProps {
  className?: string;
  children: ReactNode;
}

export function Card(props: CardProps) {
  return (
    <div
      className={cn(
        'rounded-[14px] border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)]',
        'shadow-[0_1px_2px_rgba(0,0,0,0.04)]',
        props.className,
      )}
    >
      {props.children}
    </div>
  );
}

export function CardHeader(props: { title: ReactNode; desc?: ReactNode; extra?: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-[var(--ltzy-border)] px-5 py-4">
      <div>
        <h3 className="text-sm font-semibold text-[var(--ltzy-fg)]">{props.title}</h3>
        {props.desc ? <p className="mt-0.5 text-xs text-[var(--ltzy-muted)]">{props.desc}</p> : null}
      </div>
      {props.extra}
    </div>
  );
}

export function CardBody(props: { className?: string; children: ReactNode }) {
  return <div className={cn('p-5', props.className)}>{props.children}</div>;
}

export function Badge(props: {
  tone?: 'default' | 'success' | 'warning' | 'danger' | 'primary';
  children: ReactNode;
  className?: string;
}) {
  const tone = props.tone ?? 'default';
  const map: Record<string, string> = {
    default: 'bg-[var(--ltzy-surface-2)] text-[var(--ltzy-muted)] border-[var(--ltzy-border)]',
    success: 'bg-[var(--ltzy-success)]/12 text-[var(--ltzy-success)] border-[var(--ltzy-success)]/25',
    warning: 'bg-[var(--ltzy-warning)]/12 text-[var(--ltzy-warning)] border-[var(--ltzy-warning)]/25',
    danger: 'bg-[var(--ltzy-danger)]/12 text-[var(--ltzy-danger)] border-[var(--ltzy-danger)]/25',
    primary: 'bg-[var(--ltzy-primary)]/12 text-[var(--ltzy-primary)] border-[var(--ltzy-primary)]/25',
  };
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-medium whitespace-nowrap',
        map[tone],
        props.className,
      )}
    >
      {props.children}
    </span>
  );
}

export function Empty(props: { title?: ReactNode; desc?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-14 text-center">
      <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-xl border border-dashed border-[var(--ltzy-border)] text-[var(--ltzy-muted)]">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6">
          <rect x="3" y="4" width="18" height="16" rx="2" />
          <path d="M3 10h18M8 4v16" />
        </svg>
      </div>
      <p className="text-sm font-medium text-[var(--ltzy-fg)]">{props.title ?? '暂无数据'}</p>
      {props.desc ? <p className="mt-1 max-w-sm text-xs text-[var(--ltzy-muted)]">{props.desc}</p> : null}
      {props.action ? <div className="mt-4">{props.action}</div> : null}
    </div>
  );
}

export function Skeleton(props: { className?: string }) {
  return (
    <div
      className={cn(
        'animate-pulse rounded-md bg-[var(--ltzy-surface-2)]',
        props.className,
      )}
    />
  );
}

export function Modal(props: {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  width?: string;
}) {
  if (!props.open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 sm:p-8">
      <div
        className="fixed inset-0 bg-black/55 backdrop-blur-[2px]"
        onClick={props.onClose}
        aria-hidden
      />
      <div
        role="dialog"
        aria-modal="true"
        className={cn(
          'ltzy-enter relative z-10 w-full rounded-[14px] border border-[var(--ltzy-border)]',
          'bg-[var(--ltzy-surface)] shadow-2xl',
          props.width ?? 'max-w-lg',
        )}
      >
        <div className="flex items-center justify-between border-b border-[var(--ltzy-border)] px-5 py-4">
          <h3 className="text-sm font-semibold">{props.title}</h3>
          <button
            onClick={props.onClose}
            className="rounded-md p-1 text-[var(--ltzy-muted)] hover:bg-[var(--ltzy-surface-2)] hover:text-[var(--ltzy-fg)]"
            aria-label="close"
          >
            <X size={16} />
          </button>
        </div>
        <div className="max-h-[70vh] overflow-y-auto p-5">{props.children}</div>
        {props.footer ? (
          <div className="flex justify-end gap-2 border-t border-[var(--ltzy-border)] px-5 py-3">
            {props.footer}
          </div>
        ) : null}
      </div>
    </div>
  );
}
