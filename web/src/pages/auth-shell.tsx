import { useState, type ReactNode } from 'react';
import { Link } from '@tanstack/react-router';
import { Gauge } from 'lucide-react';
import { useAppStore } from '@/stores/app';

/** 登录/注册共用的居中卡片壳 */
export function AuthShell(props: { title: ReactNode; subtitle: ReactNode; children: ReactNode; footer?: ReactNode }) {
  const status = useAppStore((s) => s.status);
  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden px-4 py-10">
      <div className="pointer-events-none absolute -top-32 left-1/2 h-72 w-[600px] -translate-x-1/2 rounded-full bg-[var(--ltzy-primary)]/15 blur-[110px]" />
      <div className="ltzy-enter relative w-full max-w-[400px]">
        <Link to="/" className="mb-6 flex items-center justify-center gap-2">
          {status?.logo ? (
            <img src={status.logo} alt="logo" className="h-9 w-9 rounded-lg object-cover" />
          ) : (
            <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--ltzy-primary)] text-white">
              <Gauge size={18} />
            </span>
          )}
          <span className="text-lg font-semibold tracking-tight">
            {status?.system_name || 'LTZY-API'}
          </span>
        </Link>
        <div className="rounded-[14px] border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)] p-6 shadow-xl">
          <h1 className="text-lg font-semibold">{props.title}</h1>
          <p className="mt-1 text-xs text-[var(--ltzy-muted)]">{props.subtitle}</p>
          <div className="mt-5">{props.children}</div>
        </div>
        {props.footer ? <div className="mt-4 text-center text-xs text-[var(--ltzy-muted)]">{props.footer}</div> : null}
      </div>
    </div>
  );
}

/** 带 OAuth 入口的登录页常用状态 */
export function useAuthPending() {
  const [pending, setPending] = useState(false);
  return { pending, setPending };
}
