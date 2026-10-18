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
import { Field, Input } from '@/components/ui/input';
import { PageHeader, StatCard } from '@/components/ui/page';

interface CreatePayOrderResult {
  out_trade_no: string;
  amount: number;
  payurl: string;
}

export default function TopupPage() {
  const { t } = useTranslation();
  const user = useAppStore((s) => s.user);
  const status = useAppStore((s) => s.status);
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const [code, setCode] = useState('');
  const [amount, setAmount] = useState('10');
  const [payType, setPayType] = useState('');
  const [pending, setPending] = useState(false);
  const [paying, setPaying] = useState(false);

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

  // 在线充值：后端建单并向易支付下单，返回可跳转的收银台地址
  const pay = async () => {
    const value = Number(amount);
    if (!Number.isFinite(value) || value <= 0) {
      toast.error(t('topup.amountInvalid'));
      return;
    }
    setPaying(true);
    try {
      const result = await mutate('/api/user/pay', 'post', {
        amount: value,
        type: payType || undefined,
      }) as unknown as { data?: CreatePayOrderResult };
      const payurl = result?.data?.payurl;
      if (!payurl) {
        toast.error(t('topup.payUrlMissing'));
        return;
      }
      window.open(payurl, '_blank', 'noopener');
      toast.success(t('topup.payOpened'));
    } catch {
      /* 已统一提示 */
    } finally {
      setPaying(false);
    }
  };

  const minTopup = status?.epay_min_topup ?? 1;

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

      {status?.epay_enabled ? (
        <Card className="mt-4">
          <CardHeader
            title={t('topup.onlineTopup')}
            desc={t('topup.onlineTopupEpayHint', { min: minTopup })}
          />
          <CardBody>
            <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
              <Field label={t('topup.amount')}>
                <Input
                  className="font-mono sm:w-40"
                  inputMode="decimal"
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                />
              </Field>
              <Field label={t('topup.payMethod')}>
                <select
                  className="h-9 rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface-2)] px-2 text-sm"
                  value={payType}
                  onChange={(e) => setPayType(e.target.value)}
                >
                  <option value="">{t('topup.useDefault')}</option>
                  <option value="alipay">{t('topup.alipay')}</option>
                  <option value="wxpay">{t('topup.wxpay')}</option>
                  <option value="qqpay">QQ</option>
                </select>
              </Field>
              <Button loading={paying} onClick={() => void pay()} icon={<CreditCard size={15} />}>
                {t('topup.goPay')}
              </Button>
            </div>
            <p className="mt-3 text-xs text-[var(--ltzy-muted)]">{t('topup.epayTip')}</p>
          </CardBody>
        </Card>
      ) : null}

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
