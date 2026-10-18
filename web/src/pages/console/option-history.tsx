import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import dayjs from 'dayjs';
import { RotateCcw, Search } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import type { OptionHistoryItem } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Badge, Card, CardHeader, Empty } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { PageHeader } from '@/components/ui/page';
import { DataTable, type Column } from '@/components/ui/table';
import { Pagination } from '@/components/ui/pagination';

// 配置变更历史与回滚（E9 前端）。
// 后端：GET /api/option/history（分页 + 按键筛选）、POST /api/option/rollback（按记录 id 回滚）。
// 语义：回滚 = 把配置恢复成该条记录的「旧值」，且回滚本身也会再写一条历史，可继续追溯。
export default function OptionHistoryPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [keyInput, setKeyInput] = useState('');
  const [key, setKey] = useState('');
  const [rollingBack, setRollingBack] = useState<number | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ['option-history', page, key],
    queryFn: () =>
      getData<{ items: OptionHistoryItem[]; total: number; p: number; page_size: number }>(
        '/api/option/history',
        { p: page, page_size: 10, ...(key ? { key } : {}) },
      ),
  });

  const items = data?.items ?? [];
  const pageSize = data?.page_size ?? 10;
  const pageCount = Math.max(1, Math.ceil((data?.total ?? 0) / pageSize));

  const rollback = async (row: OptionHistoryItem) => {
    if (!window.confirm(t('optionHistory.confirmRollback', { key: row.key }))) return;
    setRollingBack(row.id);
    try {
      await mutate('/api/option/rollback', 'post', { id: row.id });
      toast.success(t('optionHistory.rollbackDone', { key: row.key }));
      await queryClient.invalidateQueries({ queryKey: ['option-history'] });
      // 配置本身也变了（回滚会写回配置），设置页需要重新拉取
      await queryClient.invalidateQueries({ queryKey: ['options'] });
    } catch {
      // 拦截器已统一提示错误
    } finally {
      setRollingBack(null);
    }
  };

  const valueCell = (value: string, sensitive: boolean) => {
    if (!value) return <span className="text-xs text-[var(--ltzy-muted)]">{t('common.none')}</span>;
    const text = value.length > 80 ? `${value.slice(0, 80)}…` : value;
    return (
      <span className="flex items-center gap-1.5">
        {sensitive ? <Badge tone="warning">{t('optionHistory.masked')}</Badge> : null}
        <span className="font-mono text-xs break-all text-[var(--ltzy-muted)]" title={value}>
          {text}
        </span>
      </span>
    );
  };

  const columns: Column<OptionHistoryItem>[] = [
    {
      key: 'time',
      title: t('optionHistory.time'),
      render: (row) => (
        <span className="font-mono text-xs">
          {row.created_at ? dayjs(row.created_at * 1000).format('YYYY-MM-DD HH:mm:ss') : '—'}
        </span>
      ),
    },
    {
      key: 'key',
      title: t('optionHistory.key'),
      render: (row) => (
        <div>
          <span className="font-mono text-xs">{row.key}</span>
          <div className="mt-0.5 flex gap-1.5">
            {row.is_rollback ? <Badge tone="primary">{t('optionHistory.typeRollback')}</Badge> : null}
          </div>
        </div>
      ),
    },
    { key: 'old', title: t('optionHistory.oldValue'), render: (row) => valueCell(row.old_value, row.is_sensitive) },
    { key: 'new', title: t('optionHistory.newValue'), render: (row) => valueCell(row.new_value, row.is_sensitive) },
    {
      key: 'operator',
      title: t('optionHistory.operator'),
      render: (row) => (
        <span className="text-xs">
          {row.operator_name || (row.operator_id ? `#${row.operator_id}` : '—')}
        </span>
      ),
    },
    {
      key: 'actions',
      title: t('common.actions'),
      align: 'right',
      render: (row) => (
        <Button
          size="sm"
          variant="secondary"
          icon={<RotateCcw size={12} />}
          disabled={rollingBack === row.id}
          onClick={() => void rollback(row)}
        >
          {t('optionHistory.rollback')}
        </Button>
      ),
    },
  ];

  const submitSearch = () => {
    setPage(1);
    setKey(keyInput.trim());
  };

  return (
    <div>
      <PageHeader
        title={t('optionHistory.title')}
        desc={t('optionHistory.subtitle')}
        actions={
          <div className="flex items-center gap-2">
            <Input
              className="h-9 w-48"
              placeholder={t('optionHistory.filterPlaceholder')}
              value={keyInput}
              onChange={(e) => setKeyInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submitSearch();
              }}
            />
            <Button size="sm" variant="secondary" icon={<Search size={13} />} onClick={submitSearch}>
              {t('common.search')}
            </Button>
            {key ? (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setKeyInput('');
                  setKey('');
                  setPage(1);
                }}
              >
                {t('common.reset')}
              </Button>
            ) : null}
          </div>
        }
      />

      <Card>
        <CardHeader
          title={t('optionHistory.listTitle')}
          desc={t('optionHistory.listDesc')}
          extra={<Badge>{t('common.total', { count: data?.total ?? 0 })}</Badge>}
        />
        {!isLoading && items.length === 0 ? (
          <Empty title={t('common.empty')} desc={t('optionHistory.emptyDesc')} />
        ) : (
          <>
            <DataTable columns={columns} rows={items} rowKey={(r) => r.id} loading={isLoading} />
            <Pagination page={page} pageCount={pageCount} onChange={setPage} />
          </>
        )}
      </Card>
    </div>
  );
}
