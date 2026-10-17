import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { CreditCard, Ticket, Wallet } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatQuota } from '@/lib/format';
import type { User } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Card, CardBody, CardHeader } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { PageHeader, StatCard } from '@/components/ui/page';

export default function TopupPage() {
  const { t } = useTranslation();
  const user = useAppStore((s) => s.user);
  const status = useAppStore((s) => s.status);
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);

  const redeem = async () => {
    if (!code.trim()) return;
    setPending(true);
    try {
      await mutate('/api/user/topup', 'post', { key: code.trim() });
      toast.success(t('topup.redeemSuccess'));
      setCode('');
      const fresh = await getData<User>('/api/user/self');
      useAppStore.getState().setUser(fresh);
    } catch {
      /* 已统一提示 */
    } finally {
      setPending(false);
    }
  };

  return (
    <div>
      <PageHeader title={t('topup.title')} desc={t('topup.subtitle')} />

      <div className="grid gap-4 lg:grid-cols-3">
        <StatCard
          label={t('topup.currentBalance')}
          value={formatQuota(user?.quota ?? 0, quotaPerUnit)}
          icon={<Wallet size={16} />}
          tone="primary"
        />

        <Card className="lg:col-span-2">
          <CardHeader title={t('topup.redeem')} desc={t('redemption.key')} />
          <CardBody>
            <div className="flex flex-col gap-3 sm:flex-row">
              <Input
                className="font-mono"
                placeholder={t('topup.codePlaceholder')}
                value={code}
                onChange={(e) => setCode(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void redeem();
                }}
              />
              <Button loading={pending} onClick={() => void redeem()} icon={<Ticket size={15} />}>
                {t('topup.redeem')}
              </Button>
            </div>
          </CardBody>
        </Card>
      </div>

      {status?.top_up_link ? (
        <Card className="mt-4">
          <CardHeader title={t('topup.onlineTopup')} desc={t('topup.onlineTopupHint')} />
          <CardBody>
            <Button
              variant="outline"
              icon={<CreditCard size={15} />}
              onClick={() => window.open(status.top_up_link, '_blank')}
            >
              {t('topup.goPay')}
            </Button>
          </CardBody>
        </Card>
      ) : null}
    </div>
  );
}
