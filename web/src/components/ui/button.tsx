import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { cn } from '@/lib/cn';

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'outline';
type Size = 'sm' | 'md' | 'lg' | 'icon';

const variants: Record<Variant, string> = {
  primary:
    'bg-[var(--ltzy-primary)] text-[var(--ltzy-primary-fg)] hover:opacity-90 shadow-[0_1px_0_rgba(255,255,255,0.12)_inset]',
  secondary:
    'bg-[var(--ltzy-surface-2)] text-[var(--ltzy-fg)] hover:bg-[var(--ltzy-border)] border border-[var(--ltzy-border)]',
  ghost: 'text-[var(--ltzy-muted)] hover:text-[var(--ltzy-fg)] hover:bg-[var(--ltzy-surface-2)]',
  danger: 'bg-[var(--ltzy-danger)] text-white hover:opacity-90',
  outline:
    'border border-[var(--ltzy-border)] text-[var(--ltzy-fg)] hover:border-[var(--ltzy-primary)] hover:text-[var(--ltzy-primary)]',
};

const sizes: Record<Size, string> = {
  sm: 'h-8 px-3 text-xs gap-1.5',
  md: 'h-9 px-4 text-sm gap-2',
  lg: 'h-11 px-6 text-sm gap-2',
  icon: 'h-9 w-9 justify-center',
};

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  icon?: ReactNode;
}

export function Button(props: ButtonProps) {
  const { variant = 'primary', size = 'md', loading, icon, className, children, ...rest } = props;
  return (
    <button
      {...rest}
      disabled={rest.disabled || loading}
      className={cn(
        'inline-flex items-center rounded-lg font-medium transition-all duration-150',
        'focus-visible:ring-2 focus-visible:ring-[var(--ltzy-primary)]/50 focus-visible:outline-none',
        'disabled:cursor-not-allowed disabled:opacity-50',
        variants[variant],
        sizes[size],
        className,
      )}
    >
      {loading ? (
        <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-current border-t-transparent" />
      ) : (
        icon
      )}
      {children}
    </button>
  );
}
