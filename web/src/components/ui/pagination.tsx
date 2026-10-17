import { ChevronLeft, ChevronRight } from 'lucide-react';
import { Button } from './button';
import { cn } from '@/lib/cn';

export function Pagination(props: {
  page: number;
  pageCount: number;
  onChange: (page: number) => void;
  className?: string;
}) {
  const { page, pageCount, onChange } = props;
  if (pageCount <= 1) return null;
  return (
    <div className={cn('flex items-center justify-end gap-2 px-4 py-3', props.className)}>
      <span className="mr-2 text-xs text-[var(--ltzy-muted)]">
        {page} / {pageCount}
      </span>
      <Button
        variant="secondary"
        size="sm"
        disabled={page <= 1}
        onClick={() => onChange(page - 1)}
        icon={<ChevronLeft size={14} />}
      >
        Prev
      </Button>
      <Button
        variant="secondary"
        size="sm"
        disabled={page >= pageCount}
        onClick={() => onChange(page + 1)}
        icon={<ChevronRight size={14} />}
      >
        Next
      </Button>
    </div>
  );
}
