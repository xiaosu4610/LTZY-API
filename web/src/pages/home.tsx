import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  ArrowRight,
  Activity,
  Gauge,
  KeyRound,
  Layers,
  Megaphone,
  ShieldCheck,
  Sparkles,
  Terminal,
  Zap,
} from 'lucide-react';
import { useAppStore } from '@/stores/app';
import { getData } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/card';

const featureIcons = [
  <Layers key="layers" size={18} />,
  <KeyRound key="key" size={18} />,
  <Activity key="activity" size={18} />,
  <Gauge key="gauge" size={18} />,
  <ShieldCheck key="shield" size={18} />,
  <Zap key="zap" size={18} />,
];

export default function HomePage() {
  const { t } = useTranslation();
  const status = useAppStore((s) => s.status);
  const user = useAppStore((s) => s.user);

  const notice = useQuery({
    queryKey: ['notice'],
    queryFn: () => getData<string>('/api/notice'),
  });
  const content = useQuery({
    queryKey: ['home-content'],
    queryFn: () => getData<string>('/api/home_page_content'),
  });

  const features = [
    { title: '统一协议', desc: '一套 OpenAI 兼容接口，切换供应商只改 channel，不改业务代码。' },
    { title: '令牌级限权', desc: '每个密钥独立额度、模型白名单、RPM/TPM 与并发上限。' },
    { title: '全链路可观测', desc: '请求、Token、耗时、上游成本逐条落库，随时回溯。' },
    { title: '成本与毛利', desc: '按模型/用户/渠道拆解成本，收入与毛利一起看。' },
    { title: '安全与审计', desc: '权限点控制、操作审计、配置版本回滚。' },
    { title: '高可用调度', desc: '分组权重、优先级、失败重试、灰度放量与自动摘除。' },
  ];

  return (
    <div className="flex min-h-screen flex-col">
      <header className="sticky top-0 z-20 border-b border-[var(--ltzy-border)] bg-[var(--ltzy-bg)]/80 backdrop-blur-xl">
        <div className="mx-auto flex h-16 max-w-6xl items-center justify-between px-5">
          <Link to="/" className="flex items-center gap-2">
            {status?.logo ? (
              <img src={status.logo} alt="logo" className="h-8 w-8 rounded-lg object-cover" />
            ) : (
              <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-[var(--ltzy-primary)] text-white">
                <Gauge size={18} />
              </span>
            )}
            <span className="text-base font-semibold tracking-tight">
              {status?.system_name || 'LTZY-API'}
            </span>
          </Link>
          <nav className="flex items-center gap-1.5">
            <Link
              to="/about"
              className="rounded-lg px-3 py-2 text-sm text-[var(--ltzy-muted)] hover:text-[var(--ltzy-fg)]"
            >
              {t('nav.about')}
            </Link>
            {user?.id ? (
              <Button size="sm" onClick={() => (window.location.href = '/console')}>
                {t('nav.console')}
              </Button>
            ) : (
              <>
                <Button variant="ghost" size="sm" onClick={() => (window.location.href = '/login')}>
                  {t('auth.signIn')}
                </Button>
                <Button size="sm" onClick={() => (window.location.href = '/register')}>
                  {t('auth.signUp')}
                </Button>
              </>
            )}
          </nav>
        </div>
      </header>

      <main className="mx-auto w-full max-w-6xl flex-1 px-5">
        <section className="relative overflow-hidden py-20 sm:py-28">
          <div className="pointer-events-none absolute -top-24 left-1/3 h-64 w-64 rounded-full bg-[var(--ltzy-primary)]/20 blur-[100px]" />
          <Badge tone="primary" className="mb-5">
            <Sparkles size={12} /> OpenAI Compatible Gateway
          </Badge>
          <h1 className="max-w-3xl text-4xl leading-[1.15] font-semibold tracking-tight sm:text-5xl">
            {t('home.heroTitle')}
          </h1>
          <p className="mt-5 max-w-2xl text-base leading-relaxed text-[var(--ltzy-muted)]">
            {t('home.heroSubtitle')}
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Button size="lg" onClick={() => (window.location.href = user?.id ? '/console' : '/register')}>
              {t('home.getStarted')}
              <ArrowRight size={16} />
            </Button>
            <Button
              size="lg"
              variant="outline"
              onClick={() => window.open('https://github.com/xiaosu4610/LTZY-API', '_blank')}
            >
              {t('home.viewDocs')}
            </Button>
          </div>

          <div className="mt-12 max-w-2xl overflow-hidden rounded-xl border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)]">
            <div className="flex items-center gap-1.5 border-b border-[var(--ltzy-border)] px-4 py-2.5 text-xs text-[var(--ltzy-muted)]">
              <Terminal size={13} />
              <span>curl</span>
            </div>
            <pre className="overflow-x-auto p-4 font-mono text-xs leading-relaxed text-[var(--ltzy-muted)]">
              <code>{`curl ${status?.server_address || 'https://ltzy.top'}/v1/chat/completions \\
  -H "Authorization: Bearer sk-your-key" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}'`}</code>
            </pre>
          </div>
        </section>

        {notice.data ? (
          <section className="mb-14 rounded-[14px] border border-[var(--ltzy-warning)]/30 bg-[var(--ltzy-warning)]/8 p-5">
            <div className="mb-2 flex items-center gap-2 text-sm font-medium text-[var(--ltzy-warning)]">
              <Megaphone size={15} />
              {t('home.notice')}
            </div>
            <p className="text-sm whitespace-pre-wrap text-[var(--ltzy-fg)]/85">{notice.data}</p>
          </section>
        ) : null}

        <section className="pb-20">
          <h2 className="text-2xl font-semibold tracking-tight">{t('home.features')}</h2>
          <p className="mt-1.5 text-sm text-[var(--ltzy-muted)]">{t('home.featuresSubtitle')}</p>
          <div className="mt-7 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {features.map((f, i) => (
              <div
                key={f.title}
                className="group rounded-[14px] border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)] p-5 transition-all hover:-translate-y-0.5 hover:border-[var(--ltzy-primary)]/50"
              >
                <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--ltzy-primary)]/12 text-[var(--ltzy-primary)]">
                  {featureIcons[i]}
                </span>
                <h3 className="mt-3.5 text-sm font-semibold">{f.title}</h3>
                <p className="mt-1.5 text-xs leading-relaxed text-[var(--ltzy-muted)]">{f.desc}</p>
              </div>
            ))}
          </div>
        </section>

        {content.data ? (
          <section className="pb-20">
            <div
              className="prose-invert text-sm leading-relaxed text-[var(--ltzy-muted)] [&_a]:text-[var(--ltzy-primary)] [&_h2]:mt-6 [&_h2]:text-lg [&_h2]:text-[var(--ltzy-fg)]"
              dangerouslySetInnerHTML={{ __html: content.data }}
            />
          </section>
        ) : null}
      </main>

      <footer className="border-t border-[var(--ltzy-border)] px-5 py-6 text-center text-xs text-[var(--ltzy-muted)]">
        <div className="mx-auto max-w-6xl">
          {status?.footer_html ? (
            <div dangerouslySetInnerHTML={{ __html: status.footer_html }} />
          ) : (
            <span>
              LTZY-API · Based on One API (MIT) ·{' '}
              <a
                className="hover:text-[var(--ltzy-primary)]"
                href="https://github.com/xiaosu4610/LTZY-API"
                target="_blank"
                rel="noreferrer"
              >
                GitHub
              </a>
            </span>
          )}
        </div>
      </footer>
    </div>
  );
}
