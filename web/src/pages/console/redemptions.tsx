import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import dayjs from 'dayjs';
import { Plus, Trash2 } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatQuota } from '@/lib/format';
import type { Redemption } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Badge, Card, Empty, Modal } from '@/components/ui/card';
import { Field, Input } from '@/components/ui/input';
import { CopyButton } from '@/components/ui/copy-button';
import { PageHeader } from '@/components/ui/page';
import { DataTable, type Column } from '@/components/ui/table';
import { Pagination } from '@/components/ui/pagination';

export default function RedemptionsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const [page, setPage] = useState(1);
  const [keyword, setKeyword] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [form, setForm] = useState({ name: '', quota: '500000', count: '1' });

  const { data, isLoading } = useQuery({
    queryKey: ['redemptions', page, keyword],
    queryFn: () =>
      keyword.trim()
        ? getData<Redemption[]>('/api/redemption/search', { keyword: keyword.trim() })
        : getData<Redemption[]>('/api/redemption/', { p: page - 1 }),
  });

  const rows = data ?? [];

  const create = async () => {
    await mutate('/api/redemption/', 'post', {
      name: form.name,
      quota: Number(form.quota) || 0,
      count: Number(form.count) || 1,
    });
    toast.success(t('common.success'));
    setModalOpen(false);
    await queryClient.invalidateQueries({ queryKey: ['redemptions'] });
  };

  const remove = async (row: Redemption) => {
    if (!window.confirm(`${t('common.delete')} 「${row.name}」?`)) return;
    await mutate(`/api/redemption/${row.id}`, 'delete');
    await queryClient.invalidateQueries({ queryKey: ['redemptions'] });
  };

  const statusBadge = (row: Redemption) => {
    if (row.status === 1) return <Badge tone="success">{t('redemption.unused')}</Badge>;
    if (row.status === 2) return <Badge>{t('redemption.usedStatus')}</Badge>;
    return <Badge tone="danger">{t('redemption.disabledStatus')}</Badge>;
  };

  const columns: Column<Redemption>[] = [
    { key: 'name', title: t('redemption.name'), render: (row) => <span className="font-medium">{row.name}</span> },
    {
      key: 'key',
      title: t('redemption.key'),
      render: (row) => (
        <div className="flex items-center gap-1">
          <code className="rounded bg-[var(--ltzy-surface-2)] px-1.5 py-0.5 font-mono text-xs">{row.key}</code>
          <CopyButton text={row.key} />
        </div>
      ),
    },
    { key: 'status', title: t('redemption.status'), render: statusBadge },
    {
      key: 'quota',
      title: t('redemption.quota'),
      align: 'right',
      render: (row) => <span className="font-mono text-xs">{formatQuota(row.quota, quotaPerUnit)}</span>,
    },
    {
      key: 'count',
      title: t('redemption.used'),
      align: 'right',
      render: (row) => (
        <span className="font-mono text-xs">
          {row.count} / {row.count}
        </span>
      ),
    },
    {
      key: 'created',
      title: t('redemption.created'),
      render: (row) => (
        <span className="text-xs text-[var(--ltzy-muted)]">{dayjs.unix(row.created_time).format('YYYY-MM-DD HH:mm')}</span>
      ),
    },
    {
      key: 'actions',
      title: t('common.actions'),
      align: 'right',
      render: (row) => (
        <Button variant="ghost" size="icon" onClick={() => void remove(row)}>
          <Trash2 size={14} className="text-[var(--ltzy-danger)]" />
        </Button>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('redemption.title')}
        desc={t('redemption.subtitle')}
        actions={
          <>
            <Input
              className="h-9 w-56"
              placeholder={t('common.search')}
              value={keyword}
              onChange={(e) => {
                setKeyword(e.target.value);
                setPage(1);
              }}
            />
            <Button icon={<Plus size={15} />} onClick={() => setModalOpen(true)}>
              {t('common.create')}
            </Button>
          </>
        }
      />

      <Card>
        {!isLoading && rows.length === 0 ? (
          <Empty title={t('common.empty')} />
        ) : (
          <>
            <DataTable columns={columns} rows={rows} rowKey={(r) => r.id} loading={isLoading} />
            {!keyword ? <Pagination page={page} pageCount={page + (rows.length >= 10 ? 1 : 0)} onChange={setPage} /> : null}
          </>
        )}
      </Card>

      <Modal
        open={modalOpen}
        title={t('redemption.createTitle')}
        onClose={() => setModalOpen(false)}
        footer={
          <>
            <Button variant="secondary" onClick={() => setModalOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={() => void create()}>{t('common.create')}</Button>
          </>
        }
      >
        <div className="space-y-4">
          <Field label={t('redemption.name')}>
            <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </Field>
          <Field label={t('redemption.quota')} hint={`1 元 = ${quotaPerUnit} quota`}>
            <Input type="number" value={form.quota} onChange={(e) => setForm({ ...form, quota: e.target.value })} />
          </Field>
          <Field label={t('redemption.quantity')}>
            <Input type="number" value={form.count} onChange={(e) => setForm({ ...form, count: e.target.value })} />
          </Field>
        </div>
      </Modal>
    </div>
  );
}
