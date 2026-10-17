// 金额/额度换算与展示。
// 后端所有额度都以 quota 为最小单位存储，quota_per_unit 决定「1 美元 = 多少 quota」。
// 展示口径与旧版一致：quota / quota_per_unit = 金额。

export const DEFAULT_QUOTA_PER_UNIT = 500000;

export function quotaToAmount(quota: number, quotaPerUnit = DEFAULT_QUOTA_PER_UNIT): number {
  if (!quotaPerUnit) return 0;
  return quota / quotaPerUnit;
}

export function formatMoney(amount: number, fractionDigits = 4): string {
  const abs = Math.abs(amount);
  // 小额显示更多小数位，避免一片 0.0000
  const digits = abs > 0 && abs < 0.01 ? 6 : fractionDigits;
  return `$${amount.toFixed(digits)}`;
}

export function formatQuota(quota: number, quotaPerUnit = DEFAULT_QUOTA_PER_UNIT): string {
  return formatMoney(quotaToAmount(quota, quotaPerUnit));
}

export function formatNumber(value: number, compact = false): string {
  if (!Number.isFinite(value)) return '-';
  if (compact && Math.abs(value) >= 1000) {
    return new Intl.NumberFormat('en-US', {
      notation: 'compact',
      maximumFractionDigits: 1,
    }).format(value);
  }
  return new Intl.NumberFormat('en-US').format(value);
}

export function formatPercent(value: number, digits = 1): string {
  if (!Number.isFinite(value) || value < 0) return '—';
  return `${(value * 100).toFixed(digits)}%`;
}
