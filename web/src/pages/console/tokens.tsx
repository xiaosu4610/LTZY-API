import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import dayjs from 'dayjs';
import { Plus, Trash2, Pencil } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatQuota } from '@/lib/format';
import type { Token } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Badge, Card, Empty, Modal } from '@/components/ui/card';
import { Field, Input, Textarea } from '@/components/ui/input';
import { CopyButton } from '@/components/ui/copy-button';
import { PageHeader } from '@/components/ui/page';
import { DataTable, type Column } from '@/components/ui/table';
import { Pagination } from '@/components/ui/pagination';

const PAGE_SIZE_HINT = 10;

interface TokenForm {
  id?: number;
  name: string;
  remain_quota: string;
  unlimited_quota: boolean;
  expired_time: string;
  models: string;
  subnet: string;
  rpm: string;
  tpm: string;
  concurrency: string;
  daily_quota_limit: string;
  monthly_quota_limit: string;
  allowed_modes: string;
}

const emptyForm: TokenForm = {
  name: '',
  remain_quota: '0',
  unlimited_quota: false,
  expired_time: '',
  models: '',
  subnet: '',
  rpm: '0',
  tpm: '0',
  concurrency: '0',
  daily_quota_limit: '0',
  monthly_quota_limit: '0',
  allowed_modes: '',
};

export default function TokensPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const [page, setPage] = useState(1);
  const [keyword, setKeyword] = useState('');
  const [editing, setEditing] = useState<Token | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [form, setForm] = useState<TokenForm>(emptyForm);

  const queryKey = ['tokens', page, keyword];
  const { data, isLoading } = useQuery({
    queryKey,
    queryFn: async () => {
      if (keyword.trim()) {
        return getData<Token[]>(`/api/token/search`, { keyword: keyword.trim() });
      }
      return getData<Token[]>('/api/token/', { p: page - 1 });
    },
  });

  const rows = data ?? [];

  const openCreate = () => {
    setEditing(null);
    setForm(emptyForm);
    setModalOpen(true);
  };

  const openEdit = (token: Token) => {
    setEditing(token);
    setForm({
      id: token.id,
      name: token.name,
      remain_quota: String(token.remain_quota),
      unlimited_quota: token.unlimited_quota,
      expired_time: token.expired_time > 0 ? dayjs.unix(token.expired_time).format('YYYY-MM-DDTHH:mm') : '',
      models: token.models ?? '',
      subnet: token.subnet ?? '',
      rpm: String(token.rpm),
      tpm: String(token.tpm),
      concurrency: String(token.concurrency),
      daily_quota_limit: String(token.daily_quota_limit),
      monthly_quota_limit: String(token.monthly_quota_limit),
      allowed_modes: token.allowed_modes ?? '',
    });
    setModalOpen(true);
  };

  const save = useMutation({
    mutationFn: async () => {
      const payload = {
        name: form.name,
        remain_quota: Number(form.remain_quota) || 0,
        unlimited_quota: form.unlimited_quota,
        expired_time: form.expired_time ? dayjs(form.expired_time).unix() : -1,
        models: form.models,
        subnet: form.subnet,
        rpm: Number(form.rpm) || 0,
        tpm: Number(form.tpm) || 0,
        concurrency: Number(form.concurrency) || 0,
        daily_quota_limit: Number(form.daily_quota_limit) || 0,
        monthly_quota_limit: Number(form.monthly_quota_limit) || 0,
        allowed_modes: form.allowed_modes,
      };
      if (editing) {
        await mutate('/api/token/', 'put', { ...payload, id: editing.id });
      } else {
        await mutate('/api/token/', 'post', payload);
      }
    },
    onSuccess: async () => {
      toast.success(t('common.success'));
      setModalOpen(false);
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
    },
  });

  const toggleStatus = async (token: Token) => {
    await mutate('/api/token/?status_only=true', 'put', {
      id: token.id,
      status: token.status === 1 ? 2 : 1,
    });
    await queryClient.invalidateQueries({ queryKey: ['tokens'] });
  };

  const remove = async (token: Token) => {
    if (!window.confirm(`${t('common.delete')} 「${token.name}」?`)) return;
    await mutate(`/api/token/${token.id}`, 'delete');
    toast.success(t('common.success'));
    await queryClient.invalidateQueries({ queryKey: ['tokens'] });
  };

  const columns: Column<Token>[] = [
    {
      key: 'name',
      title: t('token.name'),
      render: (row) => (
        <div className="flex items-center gap-2">
          <span className="font-medium">{row.name}</span>
          {row.unlimited_quota ? <Badge tone="primary">{t('common.unlimited')}</Badge> : null}
        </div>
      ),
    },
    {
      key: 'key',
      title: t('token.key'),
      render: (row) => (
        <div className="flex items-center gap-1">
          <code className="rounded bg-[var(--ltzy-surface-2)] px-1.5 py-0.5 font-mono text-xs">
            sk-{row.key.slice(0, 6)}••••{row.key.slice(-4)}
          </code>
          <CopyButton text={`sk-${row.key}`} />
        </div>
      ),
    },
    {
      key: 'status',
      title: t('token.status'),
      render: (row) => (
        <button onClick={() => void toggleStatus(row)}>
          <Badge tone={row.status === 1 ? 'success' : 'default'}>
            {row.status === 1 ? t('common.enabled') : t('common.disabled')}
          </Badge>
        </button>
      ),
    },
    {
      key: 'quota',
      title: t('token.quota'),
      align: 'right',
      render: (row) => (
        <div className="font-mono text-xs">
          <div>{row.unlimited_quota ? t('common.unlimited') : formatQuota(row.remain_quota, quotaPerUnit)}</div>
          <div className="text-[var(--ltzy-muted)]">
            used {formatQuota(row.used_quota, quotaPerUnit)}
          </div>
        </div>
      ),
    },
    {
      key: 'limits',
      title: t('token.rpm'),
      align: 'right',
      render: (row) => (
        <span className="font-mono text-xs text-[var(--ltzy-muted)]">
          {row.rpm || '∞'} / {row.tpm || '∞'} / {row.concurrency || '∞'}
        </span>
      ),
    },
    {
      key: 'accessed',
      title: t('token.accessed'),
      render: (row) => (
        <span className="text-xs text-[var(--ltzy-muted)]">
          {row.accessed_time ? dayjs.unix(row.accessed_time).format('MM-DD HH:mm') : '—'}
        </span>
      ),
    },
    {
      key: 'expires',
      title: t('token.expires'),
      render: (row) => (
        <span className="text-xs text-[var(--ltzy-muted)]">
          {row.expired_time === -1 ? t('common.neverExpire') : dayjs.unix(row.expired_time).format('YYYY-MM-DD')}
        </span>
      ),
    },
    {
      key: 'actions',
      title: t('common.actions'),
      align: 'right',
      render: (row) => (
        <div className="flex justify-end gap-1">
          <Button variant="ghost" size="icon" onClick={() => openEdit(row)} title={t('common.edit')}>
            <Pencil size={14} />
          </Button>
          <Button variant="ghost" size="icon" onClick={() => void remove(row)} title={t('common.delete')}>
            <Trash2 size={14} className="text-[var(--ltzy-danger)]" />
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('token.title')}
        desc={t('token.subtitle')}
        actions={
          <>
            <Input
              className="h-9 w-56"
              placeholder={t('token.searchPlaceholder')}
              value={keyword}
              onChange={(e) => {
                setKeyword(e.target.value);
                setPage(1);
              }}
            />
            <Button icon={<Plus size={15} />} onClick={openCreate}>
              {t('common.create')}
            </Button>
          </>
        }
      />

      <Card>
        {!isLoading && rows.length === 0 ? (
          <Empty title={t('common.empty')} action={<Button onClick={openCreate}>{t('common.create')}</Button>} />
        ) : (
          <>
            <DataTable columns={columns} rows={rows} rowKey={(r) => r.id} loading={isLoading} />
            {!keyword ? (
              <Pagination
                page={page}
                pageCount={page + (rows.length >= PAGE_SIZE_HINT ? 1 : 0)}
                onChange={setPage}
              />
            ) : null}
          </>
        )}
      </Card>

      <Modal
        open={modalOpen}
        title={editing ? t('token.editTitle') : t('token.createTitle')}
        onClose={() => setModalOpen(false)}
        width="max-w-xl"
        footer={
          <>
            <Button variant="secondary" onClick={() => setModalOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button loading={save.isPending} onClick={() => save.mutate()}>
              {t('common.save')}
            </Button>
          </>
        }
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="sm:col-span-2">
            <Field label={t('token.name')}>
              <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
            </Field>
          </div>
          <Field label={t('token.quota')} hint={`quota，1 元 = ${quotaPerUnit}`}>
            <Input
              type="number"
              value={form.remain_quota}
              disabled={form.unlimited_quota}
              onChange={(e) => setForm({ ...form, remain_quota: e.target.value })}
            />
          </Field>
          <Field label={t('token.expires')}>
            <Input
              type="datetime-local"
              value={form.expired_time}
              onChange={(e) => setForm({ ...form, expired_time: e.target.value })}
            />
          </Field>
          <div className="flex items-center gap-2 sm:col-span-2">
            <input
              id="unlimited"
              type="checkbox"
              checked={form.unlimited_quota}
              onChange={(e) => setForm({ ...form, unlimited_quota: e.target.checked })}
              className="h-4 w-4 accent-[var(--ltzy-primary)]"
            />
            <label htmlFor="unlimited" className="text-sm">
              {t('token.unlimitedQuota')}
            </label>
          </div>
          <Field label={t('token.rpm')}>
            <Input type="number" value={form.rpm} onChange={(e) => setForm({ ...form, rpm: e.target.value })} />
          </Field>
          <Field label={t('token.tpm')}>
            <Input type="number" value={form.tpm} onChange={(e) => setForm({ ...form, tpm: e.target.value })} />
          </Field>
          <Field label={t('token.concurrency')}>
            <Input
              type="number"
              value={form.concurrency}
              onChange={(e) => setForm({ ...form, concurrency: e.target.value })}
            />
          </Field>
          <Field label={t('token.subnet')} hint="CIDR，留空不限制">
            <Input value={form.subnet} onChange={(e) => setForm({ ...form, subnet: e.target.value })} />
          </Field>
          <Field label={t('token.dailyLimit')}>
            <Input
              type="number"
              value={form.daily_quota_limit}
              onChange={(e) => setForm({ ...form, daily_quota_limit: e.target.value })}
            />
          </Field>
          <Field label={t('token.monthlyLimit')}>
            <Input
              type="number"
              value={form.monthly_quota_limit}
              onChange={(e) => setForm({ ...form, monthly_quota_limit: e.target.value })}
            />
          </Field>
          <div className="sm:col-span-2">
            <Field label={t('token.models')} hint="留空表示可用全部模型">
              <Textarea
                className="min-h-20 font-mono text-xs"
                placeholder="gpt-4o,gpt-4o-mini"
                value={form.models}
                onChange={(e) => setForm({ ...form, models: e.target.value })}
              />
            </Field>
          </div>
          <div className="sm:col-span-2">
            <Field label={t('token.allowedModes')} hint="chat,embeddings,... 留空不限制">
              <Input
                className="font-mono text-xs"
                placeholder="chat,embeddings"
                value={form.allowed_modes}
                onChange={(e) => setForm({ ...form, allowed_modes: e.target.value })}
              />
            </Field>
          </div>
        </div>
      </Modal>
    </div>
  );
}
