// 金额/额度换算与展示。
// 后端所有额度都以 quota 为最小单位存储，quota_per_unit 表示「1 个货币单位 = 多少 quota」。
// LTZY-API 统一按人民币计价：1 元 = 1,000,000 quota。

export const DEFAULT_QUOTA_PER_UNIT = 1000000;

export const CURRENCY_SYMBOL = '¥';

export function quotaToAmount(quota: number, quotaPerUnit = DEFAULT_QUOTA_PER_UNIT): number {
  if (!quotaPerUnit) return 0;
  return quota / quotaPerUnit;
}

export function formatMoney(amount: number, fractionDigits = 4): string {
  const abs = Math.abs(amount);
  // 小额显示更多小数位，避免一片 0.0000
  const digits = abs > 0 && abs < 0.01 ? 6 : fractionDigits;
  return `${CURRENCY_SYMBOL}${amount.toFixed(digits)}`;
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
