import { useState, type FormEvent } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { mutate } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { AuthShell } from './auth-shell';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/input';

export default function RegisterPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const status = useAppStore((s) => s.status);
  const [form, setForm] = useState({
    username: '',
    password: '',
    confirm: '',
    displayName: '',
    email: '',
    code: '',
  });
  const [pending, setPending] = useState(false);
  const [sending, setSending] = useState(false);

  const set = (key: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [key]: e.target.value }));

  const sendCode = async () => {
    if (!form.email) {
      toast.error('请先填写邮箱');
      return;
    }
    setSending(true);
    try {
      await mutate(`/api/verification?email=${encodeURIComponent(form.email)}`, 'get');
      toast.success('验证码已发送');
    } catch {
      /* 已统一提示 */
    } finally {
      setSending(false);
    }
  };

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (form.password !== form.confirm) {
      toast.error(t('auth.passwordMismatch'));
      return;
    }
    setPending(true);
    try {
      await mutate('/api/user/register', 'post', {
        username: form.username,
        password: form.password,
        display_name: form.displayName || form.username,
        email: form.email,
        verification_code: form.code,
      });
      toast.success(t('auth.signUpSuccess'));
      void navigate({ to: '/login' });
    } catch {
      /* 已统一提示 */
    } finally {
      setPending(false);
    }
  };

  return (
    <AuthShell
      title={t('auth.signUpTitle')}
      subtitle={t('auth.signUpSubtitle')}
      footer={
        <>
          {t('auth.hasAccount')}{' '}
          <Link to="/login" className="text-[var(--ltzy-primary)] hover:underline">
            {t('auth.signIn')}
          </Link>
        </>
      }
    >
      <form onSubmit={onSubmit} className="space-y-3.5">
        <Field label={t('auth.username')}>
          <Input value={form.username} onChange={set('username')} autoComplete="username" />
        </Field>
        <Field label={t('auth.displayName')}>
          <Input value={form.displayName} onChange={set('displayName')} />
        </Field>
        <Field label={t('auth.email')}>
          <Input type="email" value={form.email} onChange={set('email')} autoComplete="email" />
        </Field>
        {status?.email_verification ? (
          <Field label={t('auth.verificationCode')} hint={t('auth.codeSpamHint')}>
            <div className="flex gap-2">
              <Input value={form.code} onChange={set('code')} />
              <Button variant="secondary" onClick={() => void sendCode()} loading={sending}>
                {t('auth.sendCode')}
              </Button>
            </div>
          </Field>
        ) : null}
        <Field label={t('auth.password')}>
          <Input type="password" value={form.password} onChange={set('password')} autoComplete="new-password" />
        </Field>
        <Field label={t('auth.confirmPassword')}>
          <Input type="password" value={form.confirm} onChange={set('confirm')} autoComplete="new-password" />
        </Field>
        <Button type="submit" className="mt-1 w-full" size="lg" loading={pending}>
          {t('auth.signUp')}
        </Button>
      </form>
    </AuthShell>
  );
}
