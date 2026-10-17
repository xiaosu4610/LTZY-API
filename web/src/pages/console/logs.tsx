import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { getData } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatQuota } from '@/lib/format';
import { LOG_TYPE_CONSUME, LOG_TYPE_MANAGE, LOG_TYPE_SYSTEM, LOG_TYPE_TOPUP, type Log } from '@/lib/types';
import { Badge, Card, Empty } from '@/components/ui/card';
import { Input, Select } from '@/components/ui/input';
import { PageHeader } from '@/components/ui/page';
import { DataTable, type Column } from '@/components/ui/table';
import { Pagination } from '@/components/ui/pagination';

const PAGE_HINT = 10;

export default function LogsPage() {
  const { t } = useTranslation();
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const [page, setPage] = useState(1);
  const [type, setType] = useState(0);
  const [keyword, setKeyword] = useState('');
  const [self, setSelf] = useState(false);

  const { data, isLoading } = useQuery({
    queryKey: ['logs', page, type, keyword, self],
    queryFn: () => {
      const kw = keyword.trim();
      if (self) {
        return kw
          ? getData<Log[]>('/api/log/self/search', { keyword: kw })
          : getData<Log[]>('/api/log/self', { p: page - 1, type });
      }
      if (kw) {
        return getData<Log[]>('/api/log/search', { keyword: kw, type });
      }
      return getData<Log[]>('/api/log/', { p: page - 1, type });
    },
  });

  const rows = data ?? [];

  const typeBadge = (logType: number) => {
    switch (logType) {
      case LOG_TYPE_TOPUP:
        return <Badge tone="success">{t('log.typeTopup')}</Badge>;
      case LOG_TYPE_CONSUME:
        return <Badge tone="primary">{t('log.typeConsume')}</Badge>;
      case LOG_TYPE_MANAGE:
        return <Badge tone="warning">{t('log.typeManage')}</Badge>;
      case LOG_TYPE_SYSTEM:
        return <Badge tone="danger">{t('log.typeSystem')}</Badge>;
      default:
        return <Badge>—</Badge>;
    }
  };

  const columns: Column<Log>[] = [
    {
      key: 'time',
      title: t('log.time'),
      width: '150px',
      render: (row) => (
        <span className="font-mono text-xs text-[var(--ltzy-muted)]">
          {dayjs.unix(row.created_at).format('YYYY-MM-DD HH:mm:ss')}
        </span>
      ),
    },
    { key: 'type', title: t('log.type'), render: (row) => typeBadge(row.type) },
    {
      key: 'content',
      title: t('log.content'),
      render: (row) => (
        <div className="max-w-[420px]">
          <div className="truncate text-sm" title={row.content}>
            {row.content}
          </div>
          <div className="mt-0.5 flex flex-wrap gap-2 text-[11px] text-[var(--ltzy-muted)]">
            {row.model_name ? <span>model: {row.model_name}</span> : null}
            {row.token_name ? <span>key: {row.token_name}</span> : null}
            {row.channel ? <span>ch: {row.channel}</span> : null}
            {row.is_stream ? <span>stream</span> : null}
          </div>
        </div>
      ),
    },
    { key: 'user', title: t('log.user'), render: (row) => <span className="text-xs">{row.username || '—'}</span> },
    {
      key: 'tokens',
      title: t('log.tokens'),
      align: 'right',
      render: (row) =>
        row.prompt_tokens || row.completion_tokens ? (
          <span className="font-mono text-xs">
            {row.prompt_tokens} / {row.completion_tokens}
          </span>
        ) : (
          <span className="text-xs text-[var(--ltzy-muted)]">—</span>
        ),
    },
    {
      key: 'quota',
      title: t('log.quota'),
      align: 'right',
      render: (row) => (
        <span className="font-mono text-xs">{row.quota ? formatQuota(row.quota, quotaPerUnit) : '—'}</span>
      ),
    },
    {
      key: 'duration',
      title: t('log.duration'),
      align: 'right',
      render: (row) => (
        <span className="font-mono text-xs text-[var(--ltzy-muted)]">{row.use_time ? `${row.use_time}s` : '—'}</span>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('log.title')}
        desc={t('log.subtitle')}
        actions={
          <>
            <label className="flex cursor-pointer items-center gap-1.5 text-xs text-[var(--ltzy-muted)]">
              <input
                type="checkbox"
                checked={self}
                onChange={(e) => {
                  setSelf(e.target.checked);
                  setPage(1);
                }}
                className="h-3.5 w-3.5 accent-[var(--ltzy-primary)]"
              />
              only mine
            </label>
            <Select
              className="h-9 w-32"
              value={type}
              onChange={(e) => {
                setType(Number(e.target.value));
                setPage(1);
              }}
            >
              <option value={0}>{t('common.all')}</option>
              <option value={LOG_TYPE_CONSUME}>{t('log.typeConsume')}</option>
              <option value={LOG_TYPE_TOPUP}>{t('log.typeTopup')}</option>
              <option value={LOG_TYPE_MANAGE}>{t('log.typeManage')}</option>
              <option value={LOG_TYPE_SYSTEM}>{t('log.typeSystem')}</option>
            </Select>
            <Input
              className="h-9 w-56"
              placeholder={t('log.searchPlaceholder')}
              value={keyword}
              onChange={(e) => {
                setKeyword(e.target.value);
                setPage(1);
              }}
            />
          </>
        }
      />

      <Card>
        {!isLoading && rows.length === 0 ? (
          <Empty title={t('common.empty')} />
        ) : (
          <>
            <DataTable columns={columns} rows={rows} rowKey={(r) => r.id} loading={isLoading} />
            {!keyword ? (
              <Pagination page={page} pageCount={page + (rows.length >= PAGE_HINT ? 1 : 0)} onChange={setPage} />
            ) : null}
          </>
        )}
      </Card>
    </div>
  );
}
