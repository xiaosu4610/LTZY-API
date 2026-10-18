import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { CreditCard, Ticket, Wallet } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatQuota } from '@/lib/format';
import { cn } from '@/lib/cn';
import type { User } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Card, CardBody, CardHeader } from '@/components/ui/card';
import { Field, Input } from '@/components/ui/input';
import { PageHeader, StatCard } from '@/components/ui/page';

interface CreatePayOrderResult {
  out_trade_no: string;
  amount: number;
  pay_type: string;
  fee_rate: number;
  fee_amount: number;
  net_amount: number;
  quota: number;
  payurl: string;
  /** url = 可直接打开的收银台地址；qrcode = 二维码内容（微信常见），需走收银台页面 */
  pay_kind?: string;
  /** 网关收银台页面地址（submit.php），由网关自己渲染二维码 */
  cashier_url?: string;
}

/** 费率展示：0.06 → 6%，0.055 → 5.5% */
function formatRate(rate: number) {
  return `${Number((rate * 100).toFixed(4))}%`;
}

function round2(value: number) {
  return Math.round(value * 100) / 100;
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

  const channels = status?.epay_channels ?? [];
  const effectiveType = payType || status?.epay_default_type || channels[0]?.type || '';
  const currentChannel = channels.find((c) => c.type === effectiveType);

  // 前端只是「预估展示」，真正的手续费与到账额度以后端订单为准
  const preview = (() => {
    const value = Number(amount);
    const safe = Number.isFinite(value) && value > 0 ? value : 0;
    const rate = currentChannel?.fee_rate ?? 0;
    const fee = round2(safe * rate);
    const net = round2(safe - fee);
    const credited = status?.epay_fee_deduct_from_quota ? net : safe;
    return { amount: safe, fee, net, credited };
  })();

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

  // 在线充值：后端建单（含费率与实收）并向易支付下单，返回可跳转的收银台地址
  const pay = async () => {
    const value = Number(amount);
    if (!Number.isFinite(value) || value <= 0) {
      toast.error(t('topup.amountInvalid'));
      return;
    }
    setPaying(true);
    try {
      const result = (await mutate('/api/user/pay', 'post', {
        amount: value,
        type: effectiveType || undefined,
      })) as unknown as { data?: CreatePayOrderResult };
      const data = result?.data;
      if (!data) {
        toast.error(t('topup.payUrlMissing'));
        return;
      }
      // 微信在 PC 模式下常返回 weixin://… 这种「二维码内容」，浏览器直接打开没用，
      // 这种情况改走网关的收银台页面，由网关把二维码渲染出来供扫码。
      const target =
        data.pay_kind === 'url' ? data.payurl : data.cashier_url || data.payurl;
      if (!target) {
        toast.error(t('topup.payUrlMissing'));
        return;
      }
      window.open(target, '_blank', 'noopener');
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

      {status?.epay_enabled && channels.length > 0 ? (
        <Card className="mt-4">
          <CardHeader
            title={t('topup.onlineTopup')}
            desc={t('topup.onlineTopupEpayHint', { min: minTopup })}
          />
          <CardBody>
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
                <Field label={t('topup.amount')}>
                  <Input
                    className="font-mono sm:w-40"
                    inputMode="decimal"
                    value={amount}
                    onChange={(e) => setAmount(e.target.value)}
                  />
                </Field>
                <Button loading={paying} onClick={() => void pay()} icon={<CreditCard size={15} />}>
                  {t('topup.goPay')}
                </Button>
              </div>

              <div>
                <div className="mb-1.5 text-xs font-medium text-[var(--ltzy-muted)]">
                  {t('topup.payMethod')}
                </div>
                <div className="flex flex-wrap gap-2">
                  {channels.map((channel) => (
                    <button
                      key={channel.type}
                      type="button"
                      onClick={() => setPayType(channel.type)}
                      className={cn(
                        'flex items-center gap-2 rounded-lg border px-3 py-2 text-sm transition-colors',
                        effectiveType === channel.type
                          ? 'border-[var(--ltzy-primary)] bg-[var(--ltzy-primary)]/10 font-medium text-[var(--ltzy-primary)]'
                          : 'border-[var(--ltzy-border)] text-[var(--ltzy-muted)] hover:text-[var(--ltzy-fg)]',
                      )}
                    >
                      {channel.name}
                      <span className="font-mono text-xs opacity-80">
                        {t('topup.rateLabel', { rate: formatRate(channel.fee_rate) })}
                      </span>
                    </button>
                  ))}
                </div>
              </div>

              <div className="space-y-1 rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface-2)] px-3 py-2 text-xs text-[var(--ltzy-muted)]">
                <div className="flex justify-between">
                  <span>{t('topup.payAmount')}</span>
                  <span className="font-mono">¥{preview.amount.toFixed(2)}</span>
                </div>
                <div className="flex justify-between">
                  <span>{t('topup.feeAmount')}</span>
                  <span className="font-mono">¥{preview.fee.toFixed(2)}</span>
                </div>
                <div className="flex justify-between">
                  <span>{t('topup.netAmount')}</span>
                  <span className="font-mono">¥{preview.net.toFixed(2)}</span>
                </div>
                <div className="flex justify-between font-medium text-[var(--ltzy-fg)]">
                  <span>{t('topup.creditedAmount')}</span>
                  <span className="font-mono">¥{preview.credited.toFixed(2)}</span>
                </div>
              </div>
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
