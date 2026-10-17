import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { Activity, AlertTriangle, DollarSign, Gauge, TrendingUp } from 'lucide-react';
import { getData } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatNumber, formatPercent, formatQuota } from '@/lib/format';
import { channelTypeName } from '@/lib/channel-types';
import type { ChannelHealth, ChannelHealthResponse, CostAnalysis } from '@/lib/types';
import { Badge, Card, CardBody, CardHeader, Empty, Skeleton } from '@/components/ui/card';
import { Select } from '@/components/ui/input';
import { PageHeader, StatCard } from '@/components/ui/page';
import { DataTable, type Column } from '@/components/ui/table';

const WINDOWS = [
  { key: '1', hours: 1, labelKey: 'operations.last1h' },
  { key: '24', hours: 24, labelKey: 'operations.last24h' },
  { key: '168', hours: 168, labelKey: 'operations.last7d' },
  { key: '720', hours: 720, labelKey: 'operations.last30d' },
];

export default function OperationsPage() {
  const { t } = useTranslation();
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const [hours, setHours] = useState(24);

  const cost = useQuery({
    queryKey: ['cost-analysis', hours],
    queryFn: () => getData<CostAnalysis>('/api/log/cost', { hours, top: 10 }),
  });

  const health = useQuery({
    queryKey: ['channel-health', hours],
    // 后端返回的是 { hours, items } 包装对象，不是裸数组（写错会让整页 .map 崩掉）
    queryFn: () => getData<ChannelHealthResponse>('/api/channel/health', { hours }),
  });

  const summary = cost.data?.summary;
  const daily = cost.data?.daily ?? [];
  const models = cost.data?.models ?? [];
  const users = cost.data?.users ?? [];
  const channels = health.data?.items ?? [];

  const trend = daily.map((d) => ({
    day: d.day,
    revenue: d.quota,
    cost: d.cost_quota,
  }));

  const healthColumns: Column<ChannelHealth>[] = [
    {
      key: 'name',
      title: t('channel.name'),
      render: (row) => (
        <div>
          <div className="font-medium">{row.name || `#${row.channel_id}`}</div>
          <div className="text-xs text-[var(--ltzy-muted)]">
            {channelTypeName(row.type)} · #{row.channel_id}
          </div>
        </div>
      ),
    },
    {
      key: 'status',
      title: t('channel.status'),
      render: (row) => (
        <Badge tone={row.status === 1 ? 'success' : 'danger'}>
          {row.status === 1 ? t('channel.statusEnabled') : t('channel.statusAutoDisabled')}
        </Badge>
      ),
    },
    {
      key: 'success',
      title: t('operations.successRate'),
      align: 'right',
      render: (row) => (
        <span
          className={
            row.success_rate < 0
              ? 'text-xs text-[var(--ltzy-muted)]'
              : row.success_rate >= 0.95
                ? 'font-mono text-xs text-[var(--ltzy-success)]'
                : row.success_rate >= 0.8
                  ? 'font-mono text-xs text-[var(--ltzy-warning)]'
                  : 'font-mono text-xs text-[var(--ltzy-danger)]'
          }
        >
          {row.success_rate < 0 ? t('operations.noSample') : formatPercent(row.success_rate)}
        </span>
      ),
    },
    {
      key: 'latency',
      title: t('operations.avgLatency'),
      align: 'right',
      render: (row) => (
        <span className="font-mono text-xs">{row.avg_elapsed_time ? `${row.avg_elapsed_time} ms` : '—'}</span>
      ),
    },
    {
      key: 'requests',
      title: t('operations.requests'),
      align: 'right',
      render: (row) => <span className="font-mono text-xs">{formatNumber(row.request_count, true)}</span>,
    },
    {
      key: 'quota',
      title: t('operations.revenue'),
      align: 'right',
      render: (row) => <span className="font-mono text-xs">{formatQuota(row.quota, quotaPerUnit)}</span>,
    },
    {
      key: 'samples',
      title: t('operations.sampleCount'),
      align: 'right',
      render: (row) => <span className="font-mono text-xs text-[var(--ltzy-muted)]">{row.sample_count}</span>,
    },
  ];

  const chartTooltip = (props: { active?: boolean; payload?: { value: number; name: string }[]; label?: string }) => {
    if (!props.active || !props.payload?.length) return null;
    return (
      <div className="rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)] px-3 py-2 text-xs shadow-lg">
        <div className="mb-1 text-[var(--ltzy-muted)]">{props.label}</div>
        {props.payload.map((p) => (
          <div key={p.name} className="flex items-center gap-2">
            <span className="text-[var(--ltzy-muted)]">{p.name}</span>
            <span className="font-mono">{formatQuota(p.value, quotaPerUnit)}</span>
          </div>
        ))}
      </div>
    );
  };

  return (
    <div>
      <PageHeader
        title={t('operations.title')}
        desc={t('operations.subtitle')}
        actions={
          <Select className="h-9 w-36" value={hours} onChange={(e) => setHours(Number(e.target.value))}>
            {WINDOWS.map((w) => (
              <option key={w.key} value={w.hours}>
                {t(w.labelKey)}
              </option>
            ))}
          </Select>
        }
      />

      {summary && !summary.cost_configured ? (
        <div className="mb-4 flex items-center gap-2 rounded-lg border border-[var(--ltzy-warning)]/30 bg-[var(--ltzy-warning)]/8 px-4 py-2.5 text-xs text-[var(--ltzy-warning)]">
          <AlertTriangle size={14} />
          {t('operations.costNotConfigured')}
        </div>
      ) : null}

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
        <StatCard
          label={t('operations.revenue')}
          value={formatQuota(summary?.quota ?? 0, quotaPerUnit)}
          icon={<DollarSign size={16} />}
          tone="primary"
        />
        <StatCard
          label={t('operations.cost')}
          value={formatQuota(summary?.cost_quota ?? 0, quotaPerUnit)}
          icon={<TrendingUp size={16} />}
        />
        <StatCard
          label={t('operations.grossProfit')}
          value={formatQuota(summary?.gross_profit ?? 0, quotaPerUnit)}
          icon={<TrendingUp size={16} />}
          tone={(summary?.gross_profit ?? 0) >= 0 ? 'success' : 'danger'}
        />
        <StatCard
          label={t('operations.requests')}
          value={formatNumber(summary?.request_count ?? 0, true)}
          icon={<Activity size={16} />}
        />
        <StatCard
          label={t('operations.promptTokens')}
          value={formatNumber((summary?.prompt_tokens ?? 0) + (summary?.completion_tokens ?? 0), true)}
          hint={`in ${formatNumber(summary?.prompt_tokens ?? 0, true)} / out ${formatNumber(
            summary?.completion_tokens ?? 0,
            true,
          )}`}
          icon={<Gauge size={16} />}
        />
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader title={t('operations.costTrend')} />
          <CardBody className="h-[300px]">
            {cost.isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : trend.length === 0 ? (
              <Empty title={t('common.empty')} />
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={trend} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
                  <CartesianGrid stroke="var(--ltzy-border)" strokeDasharray="3 3" vertical={false} />
                  <XAxis dataKey="day" stroke="var(--ltzy-muted)" fontSize={11} tickLine={false} axisLine={false} />
                  <YAxis
                    stroke="var(--ltzy-muted)"
                    fontSize={11}
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v: number) => formatQuota(v, quotaPerUnit)}
                  />
                  <Tooltip content={chartTooltip} />
                  <Line type="monotone" dataKey="revenue" name={t('operations.revenue')} stroke="#5b8cff" strokeWidth={2} dot={false} />
                  <Line type="monotone" dataKey="cost" name={t('operations.cost')} stroke="#f87171" strokeWidth={2} dot={false} />
                </LineChart>
              </ResponsiveContainer>
            )}
          </CardBody>
        </Card>

        <Card>
          <CardHeader title={t('operations.modelRanking')} />
          <CardBody className="h-[300px]">
            {cost.isLoading ? (
              <Skeleton className="h-full w-full" />
            ) : models.length === 0 ? (
              <Empty title={t('common.empty')} />
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <BarChart
                  data={models.slice(0, 8).map((m) => ({ name: m.model_name, quota: m.quota }))}
                  layout="vertical"
                  margin={{ top: 4, right: 16, bottom: 4, left: 8 }}
                >
                  <CartesianGrid stroke="var(--ltzy-border)" strokeDasharray="3 3" horizontal={false} />
                  <XAxis
                    type="number"
                    stroke="var(--ltzy-muted)"
                    fontSize={11}
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v: number) => formatQuota(v, quotaPerUnit)}
                  />
                  <YAxis
                    type="category"
                    dataKey="name"
                    width={120}
                    stroke="var(--ltzy-muted)"
                    fontSize={11}
                    tickLine={false}
                    axisLine={false}
                  />
                  <Tooltip content={chartTooltip} />
                  <Bar dataKey="quota" name={t('operations.revenue')} fill="#5b8cff" radius={[0, 4, 4, 0]} />
                </BarChart>
              </ResponsiveContainer>
            )}
          </CardBody>
        </Card>
      </div>

      <Card className="mt-4">
        <CardHeader title={t('operations.channelHealth')} desc={`${channels.length} channels`} />
        {channels.length === 0 && !health.isLoading ? (
          <Empty title={t('common.empty')} />
        ) : (
          <DataTable columns={healthColumns} rows={channels} rowKey={(r) => r.channel_id} loading={health.isLoading} />
        )}
      </Card>

      <Card className="mt-4">
        <CardHeader title={t('operations.userRanking')} />
        {users.length === 0 && !cost.isLoading ? (
          <Empty title={t('common.empty')} />
        ) : (
          <DataTable
            loading={cost.isLoading}
            rowKey={(r) => r.user_id}
            rows={users}
            columns={[
              {
                key: 'username',
                title: t('log.user'),
                render: (row) => <span className="font-medium">{row.username || `#${row.user_id}`}</span>,
              },
              {
                key: 'requests',
                title: t('operations.requests'),
                align: 'right',
                render: (row) => <span className="font-mono text-xs">{formatNumber(row.request_count, true)}</span>,
              },
              {
                key: 'quota',
                title: t('operations.revenue'),
                align: 'right',
                render: (row) => <span className="font-mono text-xs">{formatQuota(row.quota, quotaPerUnit)}</span>,
              },
              {
                key: 'cost',
                title: t('operations.cost'),
                align: 'right',
                render: (row) => (
                  <span className="font-mono text-xs text-[var(--ltzy-muted)]">
                    {formatQuota(row.cost_quota, quotaPerUnit)}
                  </span>
                ),
              },
            ]}
          />
        )}
      </Card>
    </div>
  );
}
