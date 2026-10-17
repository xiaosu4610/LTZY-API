import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { KeySquare, RefreshCw, ShieldCheck, UserRound } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatQuota } from '@/lib/format';
import type { User } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Badge, Card, CardBody, CardHeader } from '@/components/ui/card';
import { CopyButton } from '@/components/ui/copy-button';
import { Field, Input } from '@/components/ui/input';
import { PageHeader, StatCard } from '@/components/ui/page';

export default function ProfilePage() {
  const { t } = useTranslation();
  const user = useAppStore((s) => s.user);
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const [displayName, setDisplayName] = useState(user?.display_name ?? '');
  const [oldPassword, setOldPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [accessToken, setAccessToken] = useState('');
  const [affCode, setAffCode] = useState('');

  const saveProfile = async () => {
    await mutate('/api/user/self', 'put', {
      ...user,
      display_name: displayName,
    });
    toast.success(t('common.success'));
    const fresh = await getData<User>('/api/user/self');
    useAppStore.getState().setUser(fresh);
  };

  const changePassword = async () => {
    if (!newPassword) return;
    await mutate('/api/user/self', 'put', {
      ...user,
      password: newPassword,
      original_password: oldPassword,
    });
    toast.success(t('common.success'));
    setOldPassword('');
    setNewPassword('');
  };

  const loadToken = async () => {
    const token = await getData<string>('/api/user/token');
    setAccessToken(token);
  };

  const loadAff = async () => {
    const code = await getData<string>('/api/user/aff');
    setAffCode(code);
  };

  const deleteSelf = async () => {
    if (!window.confirm(t('user.deleteSelf') + '?')) return;
    await mutate('/api/user/self', 'delete');
    window.location.href = '/login';
  };

  return (
    <div>
      <PageHeader title={t('profile.title')} desc={`${user?.username ?? ''} · #${user?.id ?? 0}`} />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          label={t('dashboard.balance')}
          value={formatQuota(user?.quota ?? 0, quotaPerUnit)}
          icon={<UserRound size={16} />}
          tone="primary"
        />
        <StatCard label={t('dashboard.used')} value={formatQuota(user?.used_quota ?? 0, quotaPerUnit)} />
        <StatCard label={t('user.requests')} value={user?.request_count ?? 0} />
        <StatCard
          label={t('user.role')}
          value={
            (user?.role ?? 0) >= 100 ? t('user.root') : (user?.role ?? 0) >= 10 ? t('user.admin') : t('user.common')
          }
        />
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader title={t('profile.account')} />
          <CardBody className="space-y-4">
            <Field label={t('auth.displayName')}>
              <Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
            </Field>
            <Field label={t('auth.email')} hint="修改邮箱需邮件验证">
              <Input value={user?.email ?? ''} disabled />
            </Field>
            <div className="flex items-center gap-2">
              <span className="text-xs text-[var(--ltzy-muted)]">{t('user.group')}</span>
              <Badge tone="primary">{user?.group || 'default'}</Badge>
            </div>
            <Button onClick={() => void saveProfile()}>{t('common.save')}</Button>
          </CardBody>
        </Card>

        <Card>
          <CardHeader title={t('profile.security')} extra={<ShieldCheck size={15} className="text-[var(--ltzy-muted)]" />} />
          <CardBody className="space-y-4">
            <Field label={t('profile.oldPassword')}>
              <Input type="password" value={oldPassword} onChange={(e) => setOldPassword(e.target.value)} />
            </Field>
            <Field label={t('profile.newPassword')}>
              <Input type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} />
            </Field>
            <Button onClick={() => void changePassword()}>{t('profile.changePassword')}</Button>

            <div className="border-t border-[var(--ltzy-border)] pt-4">
              <div className="mb-2 flex items-center justify-between">
                <span className="text-xs font-medium text-[var(--ltzy-muted)]">{t('profile.accessToken')}</span>
                <Button size="sm" variant="secondary" onClick={() => void loadToken()} icon={<RefreshCw size={12} />}>
                  {t('profile.regenerate')}
                </Button>
              </div>
              {accessToken ? (
                <div className="flex items-center gap-2 rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface-2)] px-2 py-1.5">
                  <code className="min-w-0 flex-1 truncate font-mono text-xs">{accessToken}</code>
                  <CopyButton text={accessToken} />
                </div>
              ) : (
                <p className="text-[11px] text-[var(--ltzy-muted)]">{t('profile.accessTokenHint')}</p>
              )}
            </div>
          </CardBody>
        </Card>
      </div>

      <Card className="mt-4">
        <CardHeader title={t('profile.affCode')} extra={<KeySquare size={15} className="text-[var(--ltzy-muted)]" />} />
        <CardBody>
          {affCode ? (
            <div className="flex flex-wrap items-center gap-2">
              <code className="rounded bg-[var(--ltzy-surface-2)] px-2 py-1 font-mono text-xs">{affCode}</code>
              <CopyButton text={affCode} />
              <code className="rounded bg-[var(--ltzy-surface-2)] px-2 py-1 font-mono text-xs">
                {`${window.location.origin}/register?aff=${affCode}`}
              </code>
              <CopyButton text={`${window.location.origin}/register?aff=${affCode}`} />
            </div>
          ) : (
            <Button variant="secondary" onClick={() => void loadAff()}>
              {t('profile.affCode')}
            </Button>
          )}
        </CardBody>
      </Card>

      <div className="mt-6">
        <Button variant="ghost" className="text-[var(--ltzy-danger)]" onClick={() => void deleteSelf()}>
          {t('user.deleteSelf')}
        </Button>
      </div>
    </div>
  );
}
