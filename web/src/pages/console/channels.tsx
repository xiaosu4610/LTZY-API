import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import dayjs from 'dayjs';
import { Plus, RefreshCw, Trash2, Zap } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import { formatQuota } from '@/lib/format';
import { channelTypeName } from '@/lib/channel-types';
import type { Channel } from '@/lib/types';
import { CHANNEL_STATUS_ENABLED } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Badge, Card, Empty, Modal } from '@/components/ui/card';
import { Field, Input, Textarea } from '@/components/ui/input';
import { PageHeader } from '@/components/ui/page';
import { DataTable, type Column } from '@/components/ui/table';
import { Pagination } from '@/components/ui/pagination';

interface ChannelForm {
  id?: number;
  name: string;
  type: number;
  key: string;
  base_url: string;
  models: string;
  group: string;
  priority: string;
  weight: string;
  model_mapping: string;
  status: number;
}

const emptyForm: ChannelForm = {
  name: '',
  type: 1,
  key: '',
  base_url: '',
  models: '',
  group: 'default',
  priority: '0',
  weight: '1',
  model_mapping: '',
  status: CHANNEL_STATUS_ENABLED,
};

export default function ChannelsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const [page, setPage] = useState(1);
  const [keyword, setKeyword] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [form, setForm] = useState<ChannelForm>(emptyForm);
  const [testing, setTesting] = useState<number | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ['channels', page, keyword],
    queryFn: () => {
      if (keyword.trim()) return getData<Channel[]>('/api/channel/search', { keyword: keyword.trim() });
      return getData<Channel[]>('/api/channel/', { p: page - 1 });
    },
  });

  const rows = data ?? [];

  const openCreate = () => {
    setForm(emptyForm);
    setModalOpen(true);
  };

  const openEdit = async (channel: Channel) => {
    const detail = await getData<Channel>(`/api/channel/${channel.id}`);
    setForm({
      id: detail.id,
      name: detail.name,
      type: detail.type,
      key: detail.key ?? '',
      base_url: detail.base_url ?? '',
      models: detail.models ?? '',
      group: detail.group ?? 'default',
      priority: String(detail.priority ?? 0),
      weight: String(detail.weight ?? 1),
      model_mapping: detail.model_mapping ?? '',
      status: detail.status,
    });
    setModalOpen(true);
  };

  const save = async () => {
    const payload = {
      id: form.id,
      name: form.name,
      type: form.type,
      key: form.key,
      base_url: form.base_url,
      models: form.models,
      group: form.group,
      priority: Number(form.priority) || 0,
      weight: Number(form.weight) || 1,
      model_mapping: form.model_mapping,
      status: form.status,
    };
    if (form.id) {
      await mutate('/api/channel/', 'put', payload);
    } else {
      await mutate('/api/channel/', 'post', payload);
    }
    toast.success(t('common.success'));
    setModalOpen(false);
    await queryClient.invalidateQueries({ queryKey: ['channels'] });
  };

  const remove = async (channel: Channel) => {
    if (!window.confirm(`${t('common.delete')} 「${channel.name}」?`)) return;
    await mutate(`/api/channel/${channel.id}`, 'delete');
    await queryClient.invalidateQueries({ queryKey: ['channels'] });
  };

  const test = async (channel: Channel) => {
    setTesting(channel.id);
    try {
      const result = await getData<string>(`/api/channel/test/${channel.id}`);
      toast.success(result || t('channel.testSuccess'));
      await queryClient.invalidateQueries({ queryKey: ['channels'] });
    } finally {
      setTesting(null);
    }
  };

  const statusBadge = (status: number) => {
    if (status === 1) return <Badge tone="success">{t('channel.statusEnabled')}</Badge>;
    if (status === 2) return <Badge tone="warning">{t('channel.statusManuallyDisabled')}</Badge>;
    return <Badge tone="danger">{t('channel.statusAutoDisabled')}</Badge>;
  };

  const columns: Column<Channel>[] = [
    {
      key: 'name',
      title: t('channel.name'),
      render: (row) => (
        <div>
          <div className="font-medium">{row.name}</div>
          <div className="text-xs text-[var(--ltzy-muted)]">#{row.id}</div>
        </div>
      ),
    },
    {
      key: 'type',
      title: t('channel.type'),
      render: (row) => <Badge>{channelTypeName(row.type)}</Badge>,
    },
    { key: 'status', title: t('channel.status'), render: (row) => statusBadge(row.status) },
    {
      key: 'models',
      title: t('channel.models'),
      render: (row) => {
        const list = (row.models || '').split(',').filter(Boolean);
        return (
          <span className="text-xs text-[var(--ltzy-muted)]">
            {list.length === 0 ? '—' : list.length <= 2 ? list.join(', ') : `${list.slice(0, 2).join(', ')} +${list.length - 2}`}
          </span>
        );
      },
    },
    { key: 'group', title: t('channel.group'), render: (row) => <code className="text-xs">{row.group}</code> },
    {
      key: 'priority',
      title: t('channel.priority'),
      align: 'right',
      render: (row) => (
        <span className="font-mono text-xs">
          {row.priority} / {row.weight}
        </span>
      ),
    },
    {
      key: 'balance',
      title: t('channel.balance'),
      align: 'right',
      render: (row) => (
        <span className="font-mono text-xs">
          {row.balance > 0 ? `$${(row.balance / quotaPerUnit).toFixed(2)}` : '—'}
        </span>
      ),
    },
    {
      key: 'response',
      title: t('channel.responseTime'),
      align: 'right',
      render: (row) => (
        <span className="font-mono text-xs text-[var(--ltzy-muted)]">
          {row.response_time ? `${row.response_time} ms` : '—'}
        </span>
      ),
    },
    {
      key: 'tested',
      title: t('channel.testTime'),
      render: (row) => (
        <span className="text-xs text-[var(--ltzy-muted)]">
          {row.test_time ? dayjs.unix(row.test_time).format('MM-DD HH:mm') : '—'}
        </span>
      ),
    },
    {
      key: 'actions',
      title: t('common.actions'),
      align: 'right',
      render: (row) => (
        <div className="flex justify-end gap-1">
          <Button
            variant="ghost"
            size="icon"
            loading={testing === row.id}
            onClick={() => void test(row)}
            title={t('channel.test')}
          >
            <Zap size={14} />
          </Button>
          <Button variant="ghost" size="icon" onClick={() => void openEdit(row)}>
            <RefreshCw size={14} />
          </Button>
          <Button variant="ghost" size="icon" onClick={() => void remove(row)}>
            <Trash2 size={14} className="text-[var(--ltzy-danger)]" />
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('channel.title')}
        desc={t('channel.subtitle')}
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
            {!keyword ? <Pagination page={page} pageCount={page + (rows.length >= 10 ? 1 : 0)} onChange={setPage} /> : null}
          </>
        )}
      </Card>

      <Modal
        open={modalOpen}
        title={form.id ? t('channel.editTitle') : t('channel.createTitle')}
        onClose={() => setModalOpen(false)}
        width="max-w-2xl"
        footer={
          <>
            <Button variant="secondary" onClick={() => setModalOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={() => void save()}>{t('common.save')}</Button>
          </>
        }
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('channel.name')}>
            <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </Field>
          <Field label={t('channel.type')}>
            <Input
              type="number"
              value={form.type}
              onChange={(e) => setForm({ ...form, type: Number(e.target.value) })}
            />
          </Field>
          <Field label={t('channel.baseUrl')} hint="留空使用该类型默认地址">
            <Input value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} />
          </Field>
          <Field label={t('channel.group')}>
            <Input value={form.group} onChange={(e) => setForm({ ...form, group: e.target.value })} />
          </Field>
          <div className="sm:col-span-2">
            <Field label={t('channel.keyLabel')} hint="多个密钥用换行分隔">
              <Textarea
                className="min-h-24 font-mono text-xs"
                value={form.key}
                onChange={(e) => setForm({ ...form, key: e.target.value })}
              />
            </Field>
          </div>
          <div className="sm:col-span-2">
            <Field label={t('channel.models')} hint="逗号分隔，支持通配">
              <Textarea
                className="min-h-20 font-mono text-xs"
                value={form.models}
                onChange={(e) => setForm({ ...form, models: e.target.value })}
              />
            </Field>
          </div>
          <Field label={t('channel.priority')}>
            <Input
              type="number"
              value={form.priority}
              onChange={(e) => setForm({ ...form, priority: e.target.value })}
            />
          </Field>
          <Field label={t('channel.weight')}>
            <Input type="number" value={form.weight} onChange={(e) => setForm({ ...form, weight: e.target.value })} />
          </Field>
          <div className="sm:col-span-2">
            <Field label={t('channel.modelMapping')} hint="上游模型名映射，JSON 格式">
              <Textarea
                className="min-h-20 font-mono text-xs"
                value={form.model_mapping}
                onChange={(e) => setForm({ ...form, model_mapping: e.target.value })}
              />
            </Field>
          </div>
        </div>
      </Modal>
    </div>
  );
}
