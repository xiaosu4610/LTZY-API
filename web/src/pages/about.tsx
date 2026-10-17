import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft } from 'lucide-react';
import { getData } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { Button } from '@/components/ui/button';

export default function AboutPage() {
  const { t } = useTranslation();
  const status = useAppStore((s) => s.status);
  const about = useQuery({
    queryKey: ['about'],
    queryFn: () => getData<string>('/api/about'),
  });

  return (
    <div className="flex min-h-screen flex-col">
      <header className="border-b border-[var(--ltzy-border)]">
        <div className="mx-auto flex h-16 max-w-3xl items-center justify-between px-5">
          <Link to="/" className="flex items-center gap-2 text-sm text-[var(--ltzy-muted)] hover:text-[var(--ltzy-fg)]">
            <ArrowLeft size={15} />
            {t('common.back')}
          </Link>
          <span className="text-sm font-semibold">{status?.system_name || 'LTZY-API'}</span>
        </div>
      </header>
      <main className="mx-auto w-full max-w-3xl flex-1 px-5 py-10">
        <h1 className="text-2xl font-semibold tracking-tight">{t('about.title')}</h1>
        <div className="mt-2 text-xs text-[var(--ltzy-muted)]">
          Version {status?.version || 'dev'}
        </div>
        <div className="mt-6 rounded-[14px] border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)] p-6">
          {about.data ? (
            <div
              className="text-sm leading-relaxed whitespace-pre-wrap text-[var(--ltzy-fg)]/85"
              dangerouslySetInnerHTML={{ __html: about.data }}
            />
          ) : (
            <p className="text-sm leading-relaxed text-[var(--ltzy-muted)]">
              LTZY-API 是一个基于 One API（MIT）二次开发的 LLM API 网关，提供统一的中转、计费、
              限流与可观测能力。
            </p>
          )}
        </div>
        <div className="mt-6 flex gap-2">
          <Button variant="outline" onClick={() => window.open('https://github.com/xiaosu4610/LTZY-API', '_blank')}>
            GitHub
          </Button>
          <Button variant="ghost" onClick={() => (window.location.href = '/')}>
            {t('error.goHome')}
          </Button>
        </div>
      </main>
    </div>
  );
}
