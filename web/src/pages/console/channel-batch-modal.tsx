import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { toast } from 'sonner';
import { getData, mutate } from '@/lib/api';
import { CHANNEL_TYPES } from '@/lib/channel-types';
import { Button } from '@/components/ui/button';
import { Modal } from '@/components/ui/card';
import { Field, Input, Textarea } from '@/components/ui/input';

export interface ChannelPreset {
  id: string;
  name: string;
  base_url: string;
  auth_type: string;
  models: string[];
  doc_url: string;
  note?: string;
}

interface Props {
  open: boolean;
  onClose: () => void;
  onDone: () => void;
}

/**
 * 批量导入渠道（LTZY-API 新增）。
 *
 * 典型场景：一个上游网关配了几百把密钥（如英伟达的免费额度），
 * 它们的模型列表完全一致，只是额度与限流各自独立。
 * 这里做成「共用一份上游地址/模型，粘贴多行密钥」——
 * 多行密钥会合并进**同一条渠道**，请求时轮流使用（见 model/channel_key.go）。
 * 需要各自的模型列表或进货价时，才拆成多条渠道分别填。
 */
export function ChannelBatchModal({ open, onClose, onDone }: Props) {
  const { t } = useTranslation();
  const [presetId, setPresetId] = useState('');
  const [type, setType] = useState(1);
  const [baseUrl, setBaseUrl] = useState('');
  const [group, setGroup] = useState('default');
  const [models, setModels] = useState('');
  const [keys, setKeys] = useState('');
  const [costRatio, setCostRatio] = useState('');
  const [planType, setPlanType] = useState('');
  const [pending, setPending] = useState(false);

  const { data: presets } = useQuery({
    queryKey: ['channel-presets'],
    queryFn: () => getData<ChannelPreset[]>('/api/channel/presets'),
    enabled: open,
    staleTime: 10 * 60_000,
  });

  // 选预设时把地址与常用模型带出来；已填的内容会被覆盖，这是预期的「一键填充」行为
  useEffect(() => {
    if (!presetId || !presets) return;
    const preset = presets.find((p) => p.id === presetId);
    if (!preset) return;
    setBaseUrl(preset.base_url);
    if (preset.models.length > 0) setModels(preset.models.join(','));
  }, [presetId, presets]);

  const submit = async () => {
    const keyLines = keys
      .split('\n')
      .map((k) => k.trim())
      .filter(Boolean);
    if (keyLines.length === 0) {
      toast.error(t('channelBatch.keysRequired'));
      return;
    }
    if (!models.trim()) {
      toast.error(t('channelBatch.modelsRequired'));
      return;
    }
    if (costRatio.trim()) {
      try {
        const parsed = JSON.parse(costRatio);
        if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
          throw new Error('not an object');
        }
      } catch {
        toast.error(t('channelBatch.costRatioInvalid'));
        return;
      }
    }
    setPending(true);
    try {
      await mutate('/api/channel/batch', 'post', {
        type,
        base_url: baseUrl.trim(),
        group: group.trim() || 'default',
        models: models.trim(),
        channels: [
          {
            key: keyLines.join('\n'),
            cost_ratio: costRatio.trim(),
            plan_type: planType,
          },
        ],
      });
      toast.success(t('channelBatch.imported', { count: keyLines.length }));
      setKeys('');
      onDone();
      onClose();
    } catch {
      /* 已统一提示 */
    } finally {
      setPending(false);
    }
  };

  const selectedPreset = presets?.find((p) => p.id === presetId);

  return (
    <Modal
      open={open}
      title={t('channelBatch.title')}
      onClose={onClose}
      width="max-w-2xl"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={() => void submit()} loading={pending}>
            {t('channelBatch.submit')}
          </Button>
        </>
      }
    >
      <p className="mb-4 text-xs text-[var(--ltzy-muted)]">{t('channelBatch.desc')}</p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t('channelBatch.preset')} hint={selectedPreset?.note}>
          <select
            className="h-9 w-full rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface-2)] px-2 text-sm"
            value={presetId}
            onChange={(e) => setPresetId(e.target.value)}
          >
            <option value="">{t('channelBatch.presetNone')}</option>
            {(presets ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t('channel.type')}>
          <select
            className="h-9 w-full rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface-2)] px-2 text-sm"
            value={type}
            onChange={(e) => setType(Number(e.target.value))}
          >
            {CHANNEL_TYPES.map((name, index) => (
              <option key={name} value={index + 1}>
                {index + 1} - {name}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t('channel.baseUrl')}>
          <Input
            className="font-mono"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="https://api.example.com"
          />
        </Field>
        <Field label={t('channel.group')}>
          <Input value={group} onChange={(e) => setGroup(e.target.value)} />
        </Field>
        <div className="sm:col-span-2">
          <Field label={t('channelBatch.models')} hint={t('channelBatch.modelsHint')}>
            <Textarea className="font-mono text-xs" value={models} onChange={(e) => setModels(e.target.value)} />
          </Field>
        </div>
        <div className="sm:col-span-2">
          <Field label={t('channelBatch.keys')} hint={t('channelBatch.keysHint')}>
            <Textarea className="min-h-32 font-mono text-xs" value={keys} onChange={(e) => setKeys(e.target.value)} />
          </Field>
        </div>
        <Field label={t('channelBatch.costRatio')} hint={t('channelBatch.costRatioHint')}>
          <Input className="font-mono text-xs" value={costRatio} onChange={(e) => setCostRatio(e.target.value)} placeholder='{"*":2.0}' />
        </Field>
        <Field label={t('channelBatch.planType')} hint={t('channelBatch.planTypeHint')}>
          <select
            className="h-9 w-full rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface-2)] px-2 text-sm"
            value={planType}
            onChange={(e) => setPlanType(e.target.value)}
          >
            <option value="">{t('channelBatch.planPayg')}</option>
            <option value="subscription">{t('channelBatch.planSubscription')}</option>
          </select>
        </Field>
      </div>
    </Modal>
  );
}
