import { useState, type FormEvent } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Github } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import type { User } from '@/lib/types';
import { cn } from '@/lib/cn';
import { AuthShell } from './auth-shell';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/input';

type LoginMode = 'password' | 'code';

export default function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const status = useAppStore((s) => s.status);
  const [mode, setMode] = useState<LoginMode>('password');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);
  const [sending, setSending] = useState(false);

  const codeLoginEnabled = Boolean(status?.email_code_login);

  /** 登录成功后统一收尾：拉取本人信息、写入 store、跳转控制台 */
  const afterLogin = async () => {
    const user = await getData<User>('/api/user/self');
    useAppStore.getState().setUser(user);
    toast.success(t('auth.signInSuccess'));
    void navigate({ to: '/console' });
  };

  const onSubmitPassword = async (e: FormEvent) => {
    e.preventDefault();
    if (!username || !password) return;
    setPending(true);
    try {
      await mutate('/api/user/login', 'post', { username, password });
      await afterLogin();
    } catch {
      // 错误提示已由 axios 拦截器统一弹出
    } finally {
      setPending(false);
    }
  };

  const sendCode = async () => {
    if (!email) {
      toast.error(t('auth.emailRequired'));
      return;
    }
    setSending(true);
    try {
      await mutate(`/api/verification/login?email=${encodeURIComponent(email)}`, 'get');
      toast.success(t('auth.codeSent'));
    } catch {
      // 错误提示已由 axios 拦截器统一弹出
    } finally {
      setSending(false);
    }
  };

  const onSubmitCode = async (e: FormEvent) => {
    e.preventDefault();
    if (!email || !code) return;
    setPending(true);
    try {
      await mutate('/api/user/login/email', 'post', { email, code });
      await afterLogin();
    } catch {
      // 错误提示已由 axios 拦截器统一弹出
    } finally {
      setPending(false);
    }
  };

  const oauthLogin = async (provider: 'github' | 'lark') => {
    const state = await getData<string>('/api/oauth/state');
    const clientId = provider === 'github' ? status?.github_client_id : status?.lark_client_id;
    if (!clientId) {
      toast.error(`${provider} OAuth 未配置`);
      return;
    }
    if (provider === 'github') {
      window.location.href = `https://github.com/login/oauth/authorize?client_id=${clientId}&state=${state}&scope=user:email`;
    } else {
      window.location.href = `https://open.feishu.cn/open-apis/authen/v1/index?app_id=${clientId}&redirect_uri=${encodeURIComponent(
        `${window.location.origin}/oauth/lark`,
      )}&state=${state}`;
    }
  };

  const showCodeForm = codeLoginEnabled && mode === 'code';

  return (
    <AuthShell
      title={t('auth.signInTitle')}
      subtitle={t('auth.signInSubtitle')}
      footer={
        <>
          {t('auth.noAccount')}{' '}
          <Link to="/register" className="text-[var(--ltzy-primary)] hover:underline">
            {t('auth.signUp')}
          </Link>
        </>
      }
    >
      {codeLoginEnabled ? (
        <div className="mb-4 flex gap-0.5 rounded-lg border border-[var(--ltzy-border)] p-0.5 text-xs">
          {(['password', 'code'] as LoginMode[]).map((m) => (
            <button
              key={m}
              type="button"
              onClick={() => setMode(m)}
              className={cn(
                'flex-1 rounded-md py-1.5 transition-colors',
                mode === m
                  ? 'bg-[var(--ltzy-primary)]/12 font-medium text-[var(--ltzy-primary)]'
                  : 'text-[var(--ltzy-muted)] hover:text-[var(--ltzy-fg)]',
              )}
            >
              {m === 'password' ? t('auth.passwordLogin') : t('auth.emailCodeLogin')}
            </button>
          ))}
        </div>
      ) : null}

      {showCodeForm ? (
        <form onSubmit={onSubmitCode} className="space-y-4">
          <Field label={t('auth.email')}>
            <Input
              autoFocus
              type="email"
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
            />
          </Field>
          <Field label={t('auth.verificationCode')} hint={t('auth.codeSpamHint')}>
            <div className="flex gap-2">
              <Input
                autoComplete="one-time-code"
                value={code}
                onChange={(e) => setCode(e.target.value)}
              />
              <Button type="button" variant="secondary" onClick={() => void sendCode()} loading={sending}>
                {t('auth.sendCode')}
              </Button>
            </div>
          </Field>
          <Button type="submit" className="w-full" size="lg" loading={pending}>
            {t('auth.signIn')}
          </Button>
        </form>
      ) : (
        <form onSubmit={onSubmitPassword} className="space-y-4">
          <Field label={t('auth.username')}>
            <Input
              autoFocus
              autoComplete="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="admin"
            />
          </Field>
          <Field label={t('auth.password')}>
            <Input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="••••••••"
            />
          </Field>
          <div className="flex items-center justify-between text-xs">
            <Link to="/reset" className="text-[var(--ltzy-muted)] hover:text-[var(--ltzy-primary)]">
              {t('auth.forgotPassword')}
            </Link>
          </div>
          <Button type="submit" className="w-full" size="lg" loading={pending}>
            {t('auth.signIn')}
          </Button>
        </form>
      )}

      {status?.github_oauth || status?.lark_client_id ? (
        <div className="mt-5">
          <div className="mb-3 flex items-center gap-2 text-[11px] text-[var(--ltzy-muted)]">
            <span className="h-px flex-1 bg-[var(--ltzy-border)]" />
            OAuth
            <span className="h-px flex-1 bg-[var(--ltzy-border)]" />
          </div>
          <div className="flex gap-2">
            {status?.github_oauth ? (
              <Button
                variant="secondary"
                className="flex-1"
                onClick={() => void oauthLogin('github')}
                icon={<Github size={15} />}
              >
                GitHub
              </Button>
            ) : null}
            {status?.lark_client_id ? (
              <Button variant="secondary" className="flex-1" onClick={() => void oauthLogin('lark')}>
                Lark
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}
    </AuthShell>
  );
}
