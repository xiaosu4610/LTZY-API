import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { mutate } from '@/lib/api';
import { AuthShell } from './auth-shell';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/input';

/** 找回密码：填邮箱发重置邮件 */
export default function ResetPage() {
  const { t } = useTranslation();
  const [email, setEmail] = useState('');
  const [pending, setPending] = useState(false);

  const send = async () => {
    if (!email) return;
    setPending(true);
    try {
      await mutate(`/api/reset_password?email=${encodeURIComponent(email)}`, 'get');
      toast.success(t('auth.resetSent'));
    } catch {
      /* 已统一提示 */
    } finally {
      setPending(false);
    }
  };

  return (
    <AuthShell
      title={t('auth.resetTitle')}
      subtitle={t('auth.forgotPassword')}
      footer={
        <Link to="/login" className="text-[var(--ltzy-primary)] hover:underline">
          {t('auth.signIn')}
        </Link>
      }
    >
      <div className="space-y-4">
        <Field label={t('auth.email')}>
          <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
        <Button className="w-full" size="lg" loading={pending} onClick={() => void send()}>
          {t('auth.sendCode')}
        </Button>
      </div>
    </AuthShell>
  );
}
