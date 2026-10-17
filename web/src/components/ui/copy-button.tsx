import { useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { cn } from '@/lib/cn';

export function CopyButton(props: { text: string; className?: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      title={props.label ?? 'copy'}
      onClick={async (e) => {
        e.stopPropagation();
        try {
          await navigator.clipboard.writeText(props.text);
        } catch {
          // 非 https 环境下退化为 execCommand
          const el = document.createElement('textarea');
          el.value = props.text;
          document.body.appendChild(el);
          el.select();
          document.execCommand('copy');
          el.remove();
        }
        setDone(true);
        setTimeout(() => setDone(false), 1400);
      }}
      className={cn(
        'inline-flex h-7 w-7 items-center justify-center rounded-md text-[var(--ltzy-muted)]',
        'hover:bg-[var(--ltzy-surface-2)] hover:text-[var(--ltzy-fg)]',
        props.className,
      )}
    >
      {done ? <Check size={14} className="text-[var(--ltzy-success)]" /> : <Copy size={14} />}
    </button>
  );
}
