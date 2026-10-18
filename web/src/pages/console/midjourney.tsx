import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import dayjs from 'dayjs';
import { ExternalLink, RefreshCw, Send } from 'lucide-react';
import { getData, postData } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import { formatQuota } from '@/lib/format';
import type { MidjourneyTask } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Badge, Card, CardBody, CardHeader, Empty } from '@/components/ui/card';
import { Field, Input, Select, Textarea } from '@/components/ui/input';
import { PageHeader } from '@/components/ui/page';
import { DataTable, type Column } from '@/components/ui/table';
import { Pagination } from '@/components/ui/pagination';

// Midjourney 任务页（A4 前端）。
// 后端契约：POST /api/mj/submit、GET /api/mj/tasks、GET /api/mj/task/:id/fetch；
// 管理员可切到 GET /api/mj/admin/tasks 查看全站任务。
const PROMPT_ACTIONS = ['imagine', 'blend', 'describe', 'shorten'];
const CUSTOM_ID_ACTIONS = ['upscale', 'variation', 'reroll', 'pan', 'zoom'];

const STATUS_TONE: Record<number, 'default' | 'primary' | 'warning' | 'success' | 'danger'> = {
  1: 'default',
  2: 'primary',
  3: 'warning',
  4: 'success',
  5: 'danger',
};

export default function MidjourneyPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const isAdmin = useAppStore((s) => s.isAdmin());
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());

  const [action, setAction] = useState('imagine');
  const [prompt, setPrompt] = useState('');
  const [taskId, setTaskId] = useState('');
  const [customId, setCustomId] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const [page, setPage] = useState(1);
  const [status, setStatus] = useState('');
  const [scopeAll, setScopeAll] = useState(false);
  const [fetching, setFetching] = useState<number | null>(null);

  const needsPrompt = PROMPT_ACTIONS.includes(action);
  const needsCustomId = CUSTOM_ID_ACTIONS.includes(action);
  const allScope = isAdmin && scopeAll;

  const { data, isLoading, isFetching } = useQuery({
    queryKey: ['mj-tasks', page, status, allScope],
    queryFn: () => {
      const params: Record<string, unknown> = { p: page };
      if (status) params.status = status;
      return getData<{ items: MidjourneyTask[]; total: number; page: number; page_size: number }>(
        allScope ? '/api/mj/admin/tasks' : '/api/mj/tasks',
        params,
      );
    },
  });

  const items = data?.items ?? [];
  const pageSize = data?.page_size ?? 10;
  const pageCount = Math.max(1, Math.ceil((data?.total ?? 0) / pageSize));

  const statusLabel = useMemo(
    () => ({
      1: t('midjourney.statusPending'),
      2: t('midjourney.statusSubmitted'),
      3: t('midjourney.statusInProgress'),
      4: t('midjourney.statusSuccess'),
      5: t('midjourney.statusFailure'),
    }) as Record<number, string>,
    [t],
  );

  const submit = async () => {
    if (needsPrompt && !prompt.trim()) {
      toast.error(t('midjourney.promptRequired'));
      return;
    }
    if (needsCustomId && !customId.trim()) {
      toast.error(t('midjourney.customIdRequired'));
      return;
    }
    setSubmitting(true);
    try {
      await postData<{ task_id: number; quota: number }>('/api/mj/submit', {
        action,
        ...(needsPrompt ? { prompt: prompt.trim() } : {}),
        ...(needsCustomId ? { custom_id: customId.trim(), task_id: taskId.trim() } : {}),
      });
      toast.success(t('midjourney.submitted'));
      setPrompt('');
      setCustomId('');
      setTaskId('');
      setPage(1);
      await queryClient.invalidateQueries({ queryKey: ['mj-tasks'] });
    } catch {
      // 拦截器已统一提示错误
    } finally {
      setSubmitting(false);
    }
  };

  const fetchTask = async (row: MidjourneyTask) => {
    setFetching(row.id);
    try {
      await getData<MidjourneyTask>(`/api/mj/task/${row.id}/fetch`);
      await queryClient.invalidateQueries({ queryKey: ['mj-tasks'] });
    } catch {
      // 拦截器已统一提示错误
    } finally {
      setFetching(null);
    }
  };

  const columns: Column<MidjourneyTask>[] = [
    { key: 'id', title: '#', render: (row) => <span className="font-mono text-xs">{row.id}</span> },
    ...(allScope
      ? [
          {
            key: 'user',
            title: t('midjourney.user'),
            render: (row: MidjourneyTask) => <span className="font-mono text-xs">#{row.user_id}</span>,
          } as Column<MidjourneyTask>,
        ]
      : []),
    {
      key: 'action',
      title: t('midjourney.action'),
      render: (row) => <Badge tone="primary">{row.action}</Badge>,
    },
    {
      key: 'prompt',
      title: t('midjourney.prompt'),
      render: (row) => (
        <span className="block max-w-[280px] truncate text-xs" title={row.prompt}>
          {row.prompt || <span className="text-[var(--ltzy-muted)]">{t('common.none')}</span>}
        </span>
      ),
    },
    {
      key: 'status',
      title: t('midjourney.status'),
      render: (row) => (
        <div className="flex items-center gap-1.5">
          <Badge tone={STATUS_TONE[row.status] ?? 'default'}>{statusLabel[row.status] ?? row.status}</Badge>
          {row.status === 3 ? <span className="font-mono text-[11px]">{row.progress}%</span> : null}
        </div>
      ),
    },
    {
      key: 'quota',
      title: t('midjourney.quota'),
      align: 'right',
      render: (row) => <span className="font-mono text-xs">{formatQuota(row.quota, quotaPerUnit)}</span>,
    },
    {
      key: 'image',
      title: t('midjourney.image'),
      render: (row) =>
        row.image_url ? (
          <a
            href={row.image_url}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-xs text-[var(--ltzy-primary)] hover:underline"
          >
            {t('midjourney.viewImage')}
            <ExternalLink size={11} />
          </a>
        ) : row.fail_reason ? (
          <span className="block max-w-[220px] truncate text-xs text-[var(--ltzy-danger)]" title={row.fail_reason}>
            {row.fail_reason}
          </span>
        ) : (
          <span className="text-xs text-[var(--ltzy-muted)]">—</span>
        ),
    },
    {
      key: 'submit_time',
      title: t('midjourney.submitTime'),
      render: (row) => (
        <span className="font-mono text-xs text-[var(--ltzy-muted)]">
          {row.submit_time ? dayjs(row.submit_time * 1000).format('MM-DD HH:mm') : '—'}
        </span>
      ),
    },
    {
      key: 'actions',
      title: t('common.actions'),
      align: 'right',
      render: (row) => (
        <Button
          size="sm"
          variant="secondary"
          icon={<RefreshCw size={12} />}
          disabled={fetching === row.id || row.status === 4 || row.status === 5 || !row.task_id}
          onClick={() => void fetchTask(row)}
        >
          {t('midjourney.refreshStatus')}
        </Button>
      ),
    },
  ];

  return (
    <div>
      <PageHeader title={t('midjourney.title')} desc={t('midjourney.subtitle')} />

      <div className="grid gap-4 lg:grid-cols-[360px_1fr]">
        <Card>
          <CardHeader title={t('midjourney.submitTitle')} desc={t('midjourney.submitDesc')} />
          <CardBody className="space-y-4">
            <Field label={t('midjourney.action')}>
              <Select
                value={action}
                onChange={(e) => {
                  setAction(e.target.value);
                  setPrompt('');
                  setCustomId('');
                  setTaskId('');
                }}
              >
                <optgroup label={t('midjourney.promptActions')}>
                  {PROMPT_ACTIONS.map((a) => (
                    <option key={a} value={a}>
                      {a}
                    </option>
                  ))}
                </optgroup>
                <optgroup label={t('midjourney.customIdActions')}>
                  {CUSTOM_ID_ACTIONS.map((a) => (
                    <option key={a} value={a}>
                      {a}
                    </option>
                  ))}
                </optgroup>
              </Select>
            </Field>

            {needsPrompt ? (
              <Field label={t('midjourney.prompt')} hint={t('midjourney.promptHint')}>
                <Textarea
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  placeholder="a cat sitting on a windowsill, cinematic lighting --ar 16:9"
                />
              </Field>
            ) : (
              <>
                <Field label={t('midjourney.customId')} hint={t('midjourney.customIdHint')}>
                  <Input value={customId} onChange={(e) => setCustomId(e.target.value)} />
                </Field>
                <Field label={t('midjourney.upstreamTaskId')} hint={t('midjourney.upstreamTaskIdHint')}>
                  <Input value={taskId} onChange={(e) => setTaskId(e.target.value)} />
                </Field>
              </>
            )}

            <Button variant="primary" className="w-full" loading={submitting} icon={<Send size={14} />} onClick={() => void submit()}>
              {t('midjourney.submit')}
            </Button>
          </CardBody>
        </Card>

        <Card>
          <CardHeader
            title={t('midjourney.listTitle')}
            desc={t('midjourney.listDesc')}
            extra={
              <div className="flex items-center gap-2">
                <Select
                  className="h-8 w-28"
                  value={status}
                  onChange={(e) => {
                    setStatus(e.target.value);
                    setPage(1);
                  }}
                >
                  <option value="">{t('common.all')}</option>
                  <option value="1">{t('midjourney.statusPending')}</option>
                  <option value="2">{t('midjourney.statusSubmitted')}</option>
                  <option value="3">{t('midjourney.statusInProgress')}</option>
                  <option value="4">{t('midjourney.statusSuccess')}</option>
                  <option value="5">{t('midjourney.statusFailure')}</option>
                </Select>
                {isAdmin ? (
                  <Button
                    size="sm"
                    variant={allScope ? 'primary' : 'secondary'}
                    onClick={() => {
                      setScopeAll(!allScope);
                      setPage(1);
                    }}
                  >
                    {t('midjourney.scopeAll')}
                  </Button>
                ) : null}
              </div>
            }
          />
          {!isLoading && items.length === 0 ? (
            <Empty title={t('common.empty')} desc={t('midjourney.emptyDesc')} />
          ) : (
            <>
              <DataTable
                columns={columns}
                rows={items}
                rowKey={(r) => r.id}
                loading={isLoading || isFetching}
              />
              <Pagination page={page} pageCount={pageCount} onChange={setPage} />
            </>
          )}
        </Card>
      </div>
    </div>
  );
}
