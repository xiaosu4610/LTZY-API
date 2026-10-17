import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import type { ReactNode } from 'react';
import {
  Area,
  AreaChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { Activity, Coins, KeyRound, TrendingUp } from 'lucide-react';
import { getData } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatNumber, formatQuota, quotaToAmount } from '@/lib/format';
import { Card, CardBody, CardHeader, Empty, Skeleton } from '@/components/ui/card';
import { PageHeader, StatCard } from '@/components/ui/page';

interface DashboardEntry {
  day: string;
  model_name: string;
  quota: number;
  count: number;
  prompt_tokens: number;
  completion_tokens: number;
}

const PIE_COLORS = ['#5b8cff', '#34d399', '#fbbf24', '#f87171', '#a78bfa', '#22d3ee', '#fb923c'];

function ChartTooltip(props: { active?: boolean; payload?: { value: number; name: string }[]; label?: string; isMoney?: boolean; quotaPerUnit?: number }): ReactNode {
  if (!props.active || !props.payload?.length) return null;
  const value = props.payload[0].value;
  return (
    <div className="rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)] px-3 py-2 text-xs shadow-lg">
      <div className="mb-0.5 text-[var(--ltzy-muted)]">{props.label}</div>
      <div className="font-mono">
        {props.isMoney ? formatQuota(value, props.quotaPerUnit) : formatNumber(value)}
      </div>
    </div>
  );
}

export default function DashboardPage() {
  const { t } = useTranslation();
  const user = useAppStore((s) => s.user);
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());

  const { data, isLoading } = useQuery({
    queryKey: ['dashboard'],
    queryFn: () => getData<DashboardEntry[]>('/api/user/dashboard'),
  });

  const rows = data ?? [];
  const totalQuota = rows.reduce((sum, r) => sum + r.quota, 0);
  const totalCount = rows.reduce((sum, r) => sum + r.count, 0);

  const byDay = new Map<string, number>();
  const byModel = new Map<string, number>();
  for (const r of rows) {
    byDay.set(r.day, (byDay.get(r.day) ?? 0) + r.quota);
    byModel.set(r.model_name || 'unknown', (byModel.get(r.model_name || 'unknown') ?? 0) + r.quota);
  }
  const trend = [...byDay.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([day, quota]) => ({ day: day.slice(5), quota }));
  const models = [...byModel.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, 6)
    .map(([name, quota]) => ({ name, quota }));

  const today = new Date().toISOString().slice(0, 10);
  const todayQuota = byDay.get(today) ?? 0;

  return (
    <div>
      <PageHeader
        title={t('dashboard.title')}
        desc={t('dashboard.subtitle')}
        actions={
          <span className="rounded-md border border-[var(--ltzy-border)] px-2.5 py-1 font-mono text-xs text-[var(--ltzy-muted)]">
            {user?.username}
          </span>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          label={t('dashboard.balance')}
          value={formatQuota(user?.quota ?? 0, quotaPerUnit)}
          hint={`≈ ${formatNumber(Math.round(quotaToAmount(user?.quota ?? 0, quotaPerUnit) * 100) / 100)} USD`}
          icon={<Coins size={16} />}
          tone="primary"
        />
        <StatCard
          label={t('dashboard.used')}
          value={formatQuota(user?.used_quota ?? 0, quotaPerUnit)}
          icon={<TrendingUp size={16} />}
        />
        <StatCard
          label={t('dashboard.requests')}
          value={formatNumber(user?.request_count ?? 0, true)}
          icon={<Activity size={16} />}
        />
        <StatCard
          label={t('dashboard.todayUsed')}
          value={formatQuota(todayQuota, quotaPerUnit)}
          hint={`${formatNumber(totalCount, true)} req / 7d`}
          icon={<KeyRound size={16} />}
          tone="success"
        />
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-5">
        <Card className="lg:col-span-3">
          <CardHeader
            title={t('dashboard.trend')}
            desc={`${formatQuota(totalQuota, quotaPerUnit)} · ${formatNumber(totalCount, true)} req`}
          />
          <CardBody className="h-[300px]">
            {isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : trend.length === 0 ? (
              <Empty title={t('dashboard.noData')} />
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={trend} margin={{ top: 8, right: 8, bottom: 0, left: -18 }}>
                  <defs>
                    <linearGradient id="quotaGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#5b8cff" stopOpacity={0.35} />
                      <stop offset="100%" stopColor="#5b8cff" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid stroke="var(--ltzy-border)" strokeDasharray="3 3" vertical={false} />
                  <XAxis dataKey="day" stroke="var(--ltzy-muted)" fontSize={11} tickLine={false} axisLine={false} />
                  <YAxis
                    stroke="var(--ltzy-muted)"
                    fontSize={11}
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v: number) => formatQuota(v, quotaPerUnit)}
                  />
                  <Tooltip content={<ChartTooltip isMoney quotaPerUnit={quotaPerUnit} />} />
                  <Area
                    type="monotone"
                    dataKey="quota"
                    stroke="#5b8cff"
                    strokeWidth={2}
                    fill="url(#quotaGradient)"
                  />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </CardBody>
        </Card>

        <Card className="lg:col-span-2">
          <CardHeader title={t('dashboard.byModel')} />
          <CardBody className="h-[300px]">
            {isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : models.length === 0 ? (
              <Empty title={t('dashboard.noData')} />
            ) : (
              <div className="flex h-full flex-col">
                <div className="min-h-0 flex-1">
                  <ResponsiveContainer width="100%" height="100%">
                    <PieChart>
                      <Pie
                        data={models}
                        dataKey="quota"
                        nameKey="name"
                        innerRadius="58%"
                        outerRadius="85%"
                        paddingAngle={2}
                        stroke="none"
                      >
                        {models.map((m, i) => (
                          <Cell key={m.name} fill={PIE_COLORS[i % PIE_COLORS.length]} />
                        ))}
                      </Pie>
                      <Tooltip content={<ChartTooltip isMoney quotaPerUnit={quotaPerUnit} />} />
                    </PieChart>
                  </ResponsiveContainer>
                </div>
                <div className="mt-2 space-y-1">
                  {models.map((m, i) => (
                    <div key={m.name} className="flex items-center gap-2 text-xs">
                      <span
                        className="h-2 w-2 shrink-0 rounded-full"
                        style={{ background: PIE_COLORS[i % PIE_COLORS.length] }}
                      />
                      <span className="min-w-0 flex-1 truncate text-[var(--ltzy-muted)]">{m.name}</span>
                      <span className="font-mono">{formatQuota(m.quota, quotaPerUnit)}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </CardBody>
        </Card>
      </div>
    </div>
  );
}
