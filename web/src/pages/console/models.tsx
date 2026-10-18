import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Gauge, Layers, Search, TrendingUp } from 'lucide-react';
import { getData } from '@/lib/api';
import { formatMoney, formatPercent } from '@/lib/format';
import type { ModelPlazaItem } from '@/lib/types';
import { Badge, Card, CardBody, Empty, Skeleton } from '@/components/ui/card';
import { Input, Select } from '@/components/ui/input';
import { PageHeader } from '@/components/ui/page';

// 模型广场：卡片型展示当前分组可用模型的价格与健康度。
// 数据来自 GET /api/model/plaza（与扣费同口径，价格不会与账单打架）。
type SortKey = 'name' | 'price' | 'latency';

export default function ModelsPage() {
  const { t } = useTranslation();
  const [keyword, setKeyword] = useState('');
  const [sort, setSort] = useState<SortKey>('name');

  const { data, isLoading } = useQuery({
    queryKey: ['model-plaza'],
    queryFn: () => getData<{ group: string; items: ModelPlazaItem[] }>('/api/model/plaza'),
  });

  const items = data?.items ?? [];

  const list = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    const filtered = kw ? items.filter((m) => m.model_name.toLowerCase().includes(kw)) : items;
    const sorted = [...filtered];
    if (sort === 'price') {
      sorted.sort((a, b) => a.prompt_price_per_1k - b.prompt_price_per_1k);
    } else if (sort === 'latency') {
      // 无测速样本（-1）排到最后，避免"没有数据"看起来像"最快"
      sorted.sort((a, b) => {
        const av = a.avg_response_time < 0 ? Number.MAX_SAFE_INTEGER : a.avg_response_time;
        const bv = b.avg_response_time < 0 ? Number.MAX_SAFE_INTEGER : b.avg_response_time;
        return av - bv;
      });
    }
    return sorted;
  }, [items, keyword, sort]);

  const successTone = (rate: number) => {
    if (rate < 0) return 'default' as const;
    if (rate >= 0.95) return 'success' as const;
    if (rate >= 0.8) return 'warning' as const;
    return 'danger' as const;
  };

  return (
    <div>
      <PageHeader
        title={t('models.title')}
        desc={t('models.subtitle', { group: data?.group ?? 'default' })}
        actions={
          <div className="flex items-center gap-2">
            <Input
              className="h-9 w-52"
              placeholder={t('models.searchPlaceholder')}
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
            />
            <Select className="h-9 w-36" value={sort} onChange={(e) => setSort(e.target.value as SortKey)}>
              <option value="name">{t('models.sortName')}</option>
              <option value="price">{t('models.sortPrice')}</option>
              <option value="latency">{t('models.sortLatency')}</option>
            </Select>
          </div>
        }
      />

      {isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-[168px] w-full" />
          ))}
        </div>
      ) : list.length === 0 ? (
        <Card>
          <Empty title={t('common.empty')} desc={t('models.emptyDesc')} />
        </Card>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {list.map((model) => (
            <Card key={model.model_name} className="transition-colors hover:border-[var(--ltzy-primary)]/40">
              <CardBody className="space-y-3">
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="truncate font-medium" title={model.model_name}>
                      {model.model_name}
                    </div>
                    <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-[var(--ltzy-muted)]">
                      <Layers size={11} />
                      {t('models.channelCount', { count: model.channel_count })}
                      <span className="opacity-50">·</span>
                      {t('models.ratio')} ×{model.ratio}
                    </div>
                  </div>
                  <Badge tone={successTone(model.success_rate)}>
                    {model.success_rate < 0 ? t('models.noSample') : formatPercent(model.success_rate)}
                  </Badge>
                </div>

                <div className="grid grid-cols-2 gap-2 rounded-lg bg-[var(--ltzy-surface-2)] px-3 py-2">
                  <div>
                    <div className="text-[11px] text-[var(--ltzy-muted)]">{t('models.promptPrice')}</div>
                    <div className="font-mono text-sm">{formatMoney(model.prompt_price_per_1k, 4)}</div>
                  </div>
                  <div>
                    <div className="text-[11px] text-[var(--ltzy-muted)]">{t('models.completionPrice')}</div>
                    <div className="font-mono text-sm">{formatMoney(model.completion_price_per_1k, 4)}</div>
                  </div>
                </div>

                <div className="flex items-center justify-between text-[11px] text-[var(--ltzy-muted)]">
                  <span className="inline-flex items-center gap-1">
                    <Gauge size={11} />
                    {model.avg_response_time < 0
                      ? t('models.noLatency')
                      : t('models.latency', { ms: model.avg_response_time })}
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <TrendingUp size={11} />
                    {t('models.samples', { count: model.sample_count })}
                  </span>
                </div>
              </CardBody>
            </Card>
          ))}
        </div>
      )}

      <p className="mt-4 flex items-center gap-1.5 text-[11px] text-[var(--ltzy-muted)]">
        <Search size={11} />
        {t('models.priceNote')}
      </p>
    </div>
  );
}
