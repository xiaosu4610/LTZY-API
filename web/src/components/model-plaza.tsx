import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { toast } from 'sonner';
import { Activity, Check, Copy, Gauge, Layers, Search, Timer, Zap } from 'lucide-react';
import { getData } from '@/lib/api';
import { formatMoney, formatPercent } from '@/lib/format';
import { cn } from '@/lib/cn';
import type { ModelPlazaItem } from '@/lib/types';
import { Badge, Card, CardBody, Empty, Skeleton } from '@/components/ui/card';
import { Input, Select } from '@/components/ui/input';
import { Button } from '@/components/ui/button';

// 模型广场（LTZY-API 新增）。
//
// 同一份数据有两个入口：
//   - scope="public" → GET /api/model/plaza/public（游客可见，用于站点首页）
//   - scope="me"     → GET /api/model/plaza（按登录用户所属分组）
// 组件本身只负责展示，价格/状态/速率全部由后端算好（口径与扣费一致）。

type SortKey = 'name' | 'price' | 'latency' | 'speed' | 'success';
type StatusFilter = 'all' | 'online' | 'degraded' | 'unstable';

// 供应商标识：只用于给卡片加一枚小标签，识别不出就留空，不做猜测性标注
const PROVIDER_RULES: [RegExp, string][] = [
  [/^(gpt|o1|o3|o4|chatgpt|text-embedding|dall-e|whisper|tts|text-moderation)/i, 'OpenAI'],
  [/^claude/i, 'Anthropic'],
  [/^(gemini|gemma|text-bison|palm)/i, 'Google'],
  [/^grok/i, 'xAI'],
  [/^deepseek/i, 'DeepSeek'],
  [/^(moonshot|kimi)/i, 'Moonshot'],
  [/^(glm|charglm)/i, '智谱'],
  [/^(qwen|qwq|qvq|qwen2)/i, '通义千问'],
  [/^(doubao|ep-)/i, '豆包'],
  [/^ernie/i, '文心'],
  [/^hunyuan/i, '混元'],
  [/^spark/i, '星火'],
  [/^step/i, '阶跃'],
  [/^(abab|minimax)/i, 'MiniMax'],
  [/^yi-/i, '零一万物'],
  [/^baichuan/i, '百川'],
  [/^(llama|meta-llama)/i, 'Meta'],
  [/^(mistral|mixtral|codestral|ministral|magistral|open-mistral)/i, 'Mistral'],
  [/^command/i, 'Cohere'],
  [/^(sonar|pplx)/i, 'Perplexity'],
  [/^@cf\//i, 'Cloudflare'],
  [/^(gemini|bison)/i, 'Google'],
];

function providerOf(modelName: string): string {
  const bare = modelName.split('/').pop() ?? modelName;
  for (const [pattern, label] of PROVIDER_RULES) {
    if (pattern.test(modelName) || pattern.test(bare)) return label;
  }
  return '';
}

const HEALTH_STYLE: Record<string, { dot: string; text: string; badge: 'success' | 'warning' | 'danger' }> = {
  online: { dot: 'bg-[var(--ltzy-success,#22c55e)]', text: 'text-[var(--ltzy-success,#22c55e)]', badge: 'success' },
  degraded: { dot: 'bg-[var(--ltzy-warning)]', text: 'text-[var(--ltzy-warning)]', badge: 'warning' },
  unstable: { dot: 'bg-[var(--ltzy-danger)]', text: 'text-[var(--ltzy-danger)]', badge: 'danger' },
};

interface Props {
  scope?: 'public' | 'me';
  /** 首页只给个概览，用 limit 截断；不传则全量 */
  limit?: number;
  /** 是否显示组件自带的标题与搜索工具栏（后台页面已有 PageHeader，可关掉） */
  showToolbar?: boolean;
  title?: string;
  subtitle?: string;
}

export function ModelPlaza({ scope = 'public', limit, showToolbar = true, title, subtitle }: Props) {
  const { t } = useTranslation();
  const [keyword, setKeyword] = useState('');
  const [sort, setSort] = useState<SortKey>('name');
  const [status, setStatus] = useState<StatusFilter>('all');
  const [copied, setCopied] = useState('');

  const endpoint = scope === 'me' ? '/api/model/plaza' : '/api/model/plaza/public';
  const { data, isLoading } = useQuery({
    queryKey: ['model-plaza', scope],
    queryFn: () => getData<{ group: string; items: ModelPlazaItem[] }>(endpoint),
    staleTime: 60_000,
  });

  const items = data?.items ?? [];

  const list = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    let filtered = kw
      ? items.filter((m) => m.model_name.toLowerCase().includes(kw))
      : items;
    if (status !== 'all') {
      filtered = filtered.filter((m) => m.health === status);
    }
    const sorted = [...filtered];
    switch (sort) {
      case 'price':
        sorted.sort((a, b) => a.prompt_price_per_1k - b.prompt_price_per_1k);
        break;
      case 'latency':
        // 无样本（-1）一律排到最后，避免「没有数据」看起来像「最快」
        sorted.sort((a, b) => rank(a.avg_response_time) - rank(b.avg_response_time));
        break;
      case 'speed':
        sorted.sort((a, b) => rank(-b.avg_tokens_per_second) - rank(-a.avg_tokens_per_second));
        break;
      case 'success':
        sorted.sort((a, b) => rank(-b.success_rate) - rank(-a.success_rate));
        break;
      default:
        sorted.sort((a, b) => a.model_name.localeCompare(b.model_name));
    }
    return limit ? sorted.slice(0, limit) : sorted;
  }, [items, keyword, sort, status, limit]);

  const summary = useMemo(() => {
    const onlineCount = items.filter((m) => m.health === 'online').length;
    const speeds = items.map((m) => m.avg_tokens_per_second).filter((v) => v > 0);
    const latencies = items.map((m) => m.avg_response_time).filter((v) => v > 0);
    return {
      total: items.length,
      online: onlineCount,
      avgSpeed: speeds.length ? speeds.reduce((a, b) => a + b, 0) / speeds.length : -1,
      avgLatency: latencies.length
        ? Math.round(latencies.reduce((a, b) => a + b, 0) / latencies.length)
        : -1,
      maxSpeed: speeds.length ? Math.max(...speeds) : 0,
    };
  }, [items]);

  const copyName = async (modelName: string) => {
    try {
      await navigator.clipboard.writeText(modelName);
      setCopied(modelName);
      toast.success(t('models.copied'));
      window.setTimeout(() => setCopied(''), 1500);
    } catch {
      toast.error(t('models.copyFailed'));
    }
  };

  const healthLabel = (health: string) =>
    health === 'online'
      ? t('models.healthOnline')
      : health === 'degraded'
        ? t('models.healthDegraded')
        : t('models.healthUnstable');

  return (
    <div>
      {showToolbar ? (
        <div className="mb-5 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h2 className="text-2xl font-semibold tracking-tight">
              {title ?? t('models.plazaTitle')}
            </h2>
            <p className="mt-1.5 text-sm text-[var(--ltzy-muted)]">
              {subtitle ?? t('models.plazaSubtitle', { group: data?.group ?? 'default' })}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Input
              className="h-9 w-full sm:w-48"
              placeholder={t('models.searchPlaceholder')}
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
            />
            <Select
              className="h-9 w-32"
              value={status}
              onChange={(e) => setStatus(e.target.value as StatusFilter)}
            >
              <option value="all">{t('models.statusAll')}</option>
              <option value="online">{t('models.healthOnline')}</option>
              <option value="degraded">{t('models.healthDegraded')}</option>
              <option value="unstable">{t('models.healthUnstable')}</option>
            </Select>
            <Select
              className="h-9 w-32"
              value={sort}
              onChange={(e) => setSort(e.target.value as SortKey)}
            >
              <option value="name">{t('models.sortName')}</option>
              <option value="price">{t('models.sortPrice')}</option>
              <option value="speed">{t('models.sortSpeed')}</option>
              <option value="latency">{t('models.sortLatency')}</option>
              <option value="success">{t('models.sortSuccess')}</option>
            </Select>
          </div>
        </div>
      ) : null}

      {isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: limit ?? 6 }).map((_, i) => (
            <Skeleton key={i} className="h-[196px] w-full" />
          ))}
        </div>
      ) : list.length === 0 ? (
        <Card>
          <Empty title={t('common.empty')} desc={t('models.emptyDesc')} />
        </Card>
      ) : (
        <>
          {!limit && summary.total > 0 ? (
            <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
              <MiniStat label={t('models.statTotal')} value={String(summary.total)} />
              <MiniStat
                label={t('models.statOnline')}
                value={`${summary.online}/${summary.total}`}
                tone={summary.online === summary.total ? 'success' : 'warning'}
              />
              <MiniStat
                label={t('models.statSpeed')}
                value={summary.avgSpeed < 0 ? t('models.noSample') : `${summary.avgSpeed.toFixed(1)} t/s`}
              />
              <MiniStat
                label={t('models.statLatency')}
                value={summary.avgLatency < 0 ? t('models.noSample') : `${summary.avgLatency} ms`}
              />
            </div>
          ) : null}

          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {list.map((model) => (
              <ModelCard
                key={model.model_name}
                model={model}
                maxSpeed={summary.maxSpeed}
                healthLabel={healthLabel(model.health)}
                provider={providerOf(model.model_name)}
                copied={copied === model.model_name}
                onCopy={() => void copyName(model.model_name)}
              />
            ))}
          </div>
        </>
      )}

      {!limit && list.length > 0 ? (
        <p className="mt-4 flex items-center gap-1.5 text-[11px] text-[var(--ltzy-muted)]">
          <Search size={11} />
          {t('models.priceNote')}
        </p>
      ) : null}
    </div>
  );
}

function rank(value: number) {
  return value < 0 ? Number.MAX_SAFE_INTEGER : value;
}

function MiniStat({
  label,
  value,
  tone = 'default',
}: {
  label: string;
  value: string;
  tone?: 'default' | 'success' | 'warning';
}) {
  const toneClass =
    tone === 'success'
      ? 'text-[var(--ltzy-success,#22c55e)]'
      : tone === 'warning'
        ? 'text-[var(--ltzy-warning)]'
        : 'text-[var(--ltzy-fg)]';
  return (
    <div className="rounded-[14px] border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)] px-4 py-3">
      <div className="text-[11px] text-[var(--ltzy-muted)]">{label}</div>
      <div className={cn('mt-1 font-mono text-lg font-semibold', toneClass)}>{value}</div>
    </div>
  );
}

function ModelCard({
  model,
  maxSpeed,
  healthLabel,
  provider,
  copied,
  onCopy,
}: {
  model: ModelPlazaItem;
  maxSpeed: number;
  healthLabel: string;
  provider: string;
  copied: boolean;
  onCopy: () => void;
}) {
  const { t } = useTranslation();
  const style = HEALTH_STYLE[model.health] ?? HEALTH_STYLE.online;
  const hasSpeed = model.avg_tokens_per_second > 0;
  const speedPercent = hasSpeed && maxSpeed > 0
    ? Math.max(6, Math.round((model.avg_tokens_per_second / maxSpeed) * 100))
    : 0;

  return (
    <Card className="group transition-all hover:-translate-y-0.5 hover:border-[var(--ltzy-primary)]/50">
      <CardBody className="space-y-3.5">
        {/* 头部：状态灯 + 模型名 + 供应商 */}
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span
                className={cn('relative flex h-2 w-2 shrink-0 rounded-full', style.dot)}
                title={healthLabel}
              >
                {model.health === 'online' ? (
                  <span className={cn('absolute inset-0 animate-ping rounded-full opacity-60', style.dot)} />
                ) : null}
              </span>
              <span className="truncate font-mono text-sm font-medium" title={model.model_name}>
                {model.model_name}
              </span>
            </div>
            <div className="mt-1.5 flex flex-wrap items-center gap-1.5 text-[11px] text-[var(--ltzy-muted)]">
              <span className="inline-flex items-center gap-1">
                <Layers size={11} />
                {t('models.channelCount', { count: model.channel_count })}
              </span>
              <span className="opacity-40">·</span>
              <span className={style.text}>{healthLabel}</span>
            </div>
          </div>
          <div className="flex shrink-0 flex-col items-end gap-1.5">
            {provider ? <Badge>{provider}</Badge> : null}
            <Button
              variant="ghost"
              size="sm"
              className="h-6 px-1.5 text-[11px] opacity-0 transition-opacity group-hover:opacity-100"
              onClick={onCopy}
              icon={copied ? <Check size={11} /> : <Copy size={11} />}
            >
              {copied ? t('models.copied') : t('models.copy')}
            </Button>
          </div>
        </div>

        {/* 价格 */}
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

        {/* 速率条：相对同页最快模型的比例，用来一眼看出快慢 */}
        <div>
          <div className="mb-1 flex items-center justify-between text-[11px] text-[var(--ltzy-muted)]">
            <span className="inline-flex items-center gap-1">
              <Zap size={11} />
              {t('models.tokensPerSecond')}
            </span>
            <span className="font-mono text-[var(--ltzy-fg)]">
              {hasSpeed ? `${model.avg_tokens_per_second.toFixed(1)} t/s` : t('models.noSample')}
            </span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--ltzy-surface-2)]">
            <div
              className="h-full rounded-full bg-[var(--ltzy-primary)] transition-all"
              style={{ width: `${speedPercent}%` }}
            />
          </div>
        </div>

        {/* 延迟 + 成功率 */}
        <div className="flex items-center justify-between border-t border-[var(--ltzy-border)] pt-3 text-[11px]">
          <span className="inline-flex items-center gap-1 text-[var(--ltzy-muted)]">
            <Timer size={11} />
            {model.avg_response_time < 0
              ? t('models.noLatency')
              : t('models.latency', { ms: model.avg_response_time })}
          </span>
          <span className="inline-flex items-center gap-1 text-[var(--ltzy-muted)]">
            <Activity size={11} />
            {model.success_rate < 0 ? (
              t('models.noSample')
            ) : (
              <span className={style.text}>{formatPercent(model.success_rate)}</span>
            )}
          </span>
          {model.sample_count > 0 ? (
            <span className="inline-flex items-center gap-1 text-[var(--ltzy-muted)]">
              <Gauge size={11} />
              {model.sample_count}
            </span>
          ) : null}
        </div>
      </CardBody>
    </Card>
  );
}
