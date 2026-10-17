import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { Plus, Trash2, UserCog } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatQuota } from '@/lib/format';
import type { User } from '@/lib/types';
import { ROLE_ADMIN, ROLE_COMMON, ROLE_ROOT } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Badge, Card, Empty, Modal } from '@/components/ui/card';
import { Field, Input, Select } from '@/components/ui/input';
import { PageHeader } from '@/components/ui/page';
import { DataTable, type Column } from '@/components/ui/table';
import { Pagination } from '@/components/ui/pagination';

interface UserForm {
  id?: number;
  username: string;
  display_name: string;
  password: string;
  email: string;
  role: number;
  group: string;
  quota: string;
}

const emptyForm: UserForm = {
  username: '',
  display_name: '',
  password: '',
  email: '',
  role: ROLE_COMMON,
  group: 'default',
  quota: '0',
};

export default function UsersPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const [page, setPage] = useState(1);
  const [keyword, setKeyword] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [form, setForm] = useState<UserForm>(emptyForm);

  const { data, isLoading } = useQuery({
    queryKey: ['users', page, keyword],
    queryFn: () =>
      keyword.trim()
        ? getData<User[]>('/api/user/search', { keyword: keyword.trim() })
        : getData<User[]>('/api/user/', { p: page - 1 }),
  });

  const rows = data ?? [];

  const openCreate = () => {
    setForm(emptyForm);
    setModalOpen(true);
  };

  const openEdit = async (user: User) => {
    const detail = await getData<User>(`/api/user/${user.id}`);
    setForm({
      id: detail.id,
      username: detail.username,
      display_name: detail.display_name,
      password: '',
      email: detail.email,
      role: detail.role,
      group: detail.group,
      quota: String(detail.quota),
    });
    setModalOpen(true);
  };

  const save = async () => {
    const payload = {
      id: form.id,
      username: form.username,
      display_name: form.display_name,
      password: form.password,
      email: form.email,
      role: form.role,
      group: form.group,
      quota: Number(form.quota) || 0,
    };
    if (form.id) {
      await mutate('/api/user/', 'put', payload);
    } else {
      await mutate('/api/user/', 'post', payload);
    }
    toast.success(t('common.success'));
    setModalOpen(false);
    await queryClient.invalidateQueries({ queryKey: ['users'] });
  };

  const toggleStatus = async (user: User) => {
    await mutate('/api/user/manage', 'post', {
      id: user.id,
      action: user.status === 1 ? 'disable' : 'enable',
    });
    await queryClient.invalidateQueries({ queryKey: ['users'] });
  };

  const remove = async (user: User) => {
    if (!window.confirm(`${t('common.delete')} 「${user.username}」?`)) return;
    await mutate(`/api/user/${user.id}`, 'delete');
    await queryClient.invalidateQueries({ queryKey: ['users'] });
  };

  const roleBadge = (role: number) => {
    if (role >= ROLE_ROOT) return <Badge tone="danger">{t('user.root')}</Badge>;
    if (role >= ROLE_ADMIN) return <Badge tone="warning">{t('user.admin')}</Badge>;
    return <Badge>{t('user.common')}</Badge>;
  };

  const columns: Column<User>[] = [
    {
      key: 'username',
      title: t('user.username'),
      render: (row) => (
        <div>
          <div className="font-medium">{row.username}</div>
          <div className="text-xs text-[var(--ltzy-muted)]">{row.display_name || `#${row.id}`}</div>
        </div>
      ),
    },
    { key: 'role', title: t('user.role'), render: (row) => roleBadge(row.role) },
    { key: 'group', title: t('user.group'), render: (row) => <code className="text-xs">{row.group}</code> },
    {
      key: 'status',
      title: t('user.status'),
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
      title: t('user.quota'),
      align: 'right',
      render: (row) => <span className="font-mono text-xs">{formatQuota(row.quota, quotaPerUnit)}</span>,
    },
    {
      key: 'used',
      title: t('user.usedQuota'),
      align: 'right',
      render: (row) => (
        <span className="font-mono text-xs text-[var(--ltzy-muted)]">{formatQuota(row.used_quota, quotaPerUnit)}</span>
      ),
    },
    {
      key: 'requests',
      title: t('user.requests'),
      align: 'right',
      render: (row) => <span className="font-mono text-xs">{row.request_count}</span>,
    },
    {
      key: 'actions',
      title: t('common.actions'),
      align: 'right',
      render: (row) => (
        <div className="flex justify-end gap-1">
          <Button variant="ghost" size="icon" onClick={() => void openEdit(row)} title={t('common.edit')}>
            <UserCog size={14} />
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
        title={t('user.title')}
        desc={t('user.subtitle')}
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
        title={form.id ? t('user.editTitle') : t('user.createTitle')}
        onClose={() => setModalOpen(false)}
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
          <Field label={t('user.username')}>
            <Input value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
          </Field>
          <Field label={t('user.displayName')}>
            <Input value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} />
          </Field>
          <Field label={t('auth.password')} hint={form.id ? '留空表示不修改' : undefined}>
            <Input
              type="password"
              value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })}
            />
          </Field>
          <Field label={t('user.email')}>
            <Input value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
          </Field>
          <Field label={t('user.role')}>
            <Select value={form.role} onChange={(e) => setForm({ ...form, role: Number(e.target.value) })}>
              <option value={ROLE_COMMON}>{t('user.common')}</option>
              <option value={ROLE_ADMIN}>{t('user.admin')}</option>
              <option value={ROLE_ROOT}>{t('user.root')}</option>
            </Select>
          </Field>
          <Field label={t('user.group')}>
            <Input value={form.group} onChange={(e) => setForm({ ...form, group: e.target.value })} />
          </Field>
          <div className="sm:col-span-2">
            <Field label={t('user.quota')} hint={`1 USD = ${quotaPerUnit} quota`}>
              <Input type="number" value={form.quota} onChange={(e) => setForm({ ...form, quota: e.target.value })} />
            </Field>
          </div>
        </div>
      </Modal>
    </div>
  );
}
