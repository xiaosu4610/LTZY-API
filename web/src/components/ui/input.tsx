import type { InputHTMLAttributes, SelectHTMLAttributes, TextareaHTMLAttributes } from 'react';
import { cn } from '@/lib/cn';

const base =
  'w-full rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface-2)] px-3 text-sm ' +
  'text-[var(--ltzy-fg)] placeholder:text-[var(--ltzy-muted)]/70 transition-colors ' +
  'focus:border-[var(--ltzy-primary)] focus:ring-2 focus:ring-[var(--ltzy-primary)]/20 focus:outline-none ' +
  'disabled:cursor-not-allowed disabled:opacity-60';

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  const { className, ...rest } = props;
  return <input {...rest} className={cn(base, 'h-9', className)} />;
}

export function Textarea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  const { className, ...rest } = props;
  return <textarea {...rest} className={cn(base, 'min-h-24 py-2 leading-relaxed', className)} />;
}

export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  const { className, children, ...rest } = props;
  return (
    <select {...rest} className={cn(base, 'h-9 pr-8', className)}>
      {children}
    </select>
  );
}

export function Label({ children, htmlFor }: { children: React.ReactNode; htmlFor?: string }) {
  return (
    <label htmlFor={htmlFor} className="mb-1.5 block text-xs font-medium text-[var(--ltzy-muted)]">
      {children}
    </label>
  );
}

export function Field(props: { label?: React.ReactNode; hint?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div>
      {props.label ? <Label>{props.label}</Label> : null}
      {props.children}
      {props.hint ? (
        <p className="mt-1.5 text-xs text-[var(--ltzy-muted)]">{props.hint}</p>
      ) : null}
    </div>
  );
}
