import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';

export interface Column<T> {
  key: string;
  title: ReactNode;
  width?: string;
  align?: 'left' | 'right' | 'center';
  render: (row: T, index: number) => ReactNode;
}

interface TableProps<T> {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string | number;
  loading?: boolean;
  skeletonRows?: number;
}

export function DataTable<T>(props: TableProps<T>) {
  const { columns, rows, rowKey, loading, skeletonRows = 6 } = props;

  return (
    <div className="w-full overflow-x-auto">
      <table className="w-full min-w-[720px] border-collapse text-sm">
        <thead>
          <tr className="border-b border-[var(--ltzy-border)]">
            {columns.map((col) => (
              <th
                key={col.key}
                style={col.width ? { width: col.width } : undefined}
                className={cn(
                  'px-4 py-3 text-xs font-medium whitespace-nowrap text-[var(--ltzy-muted)]',
                  col.align === 'right' && 'text-right',
                  col.align === 'center' && 'text-center',
                  (!col.align || col.align === 'left') && 'text-left',
                )}
              >
                {col.title}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {loading
            ? Array.from({ length: skeletonRows }).map((_, i) => (
                <tr key={`sk-${i}`} className="border-b border-[var(--ltzy-border)]/60">
                  {columns.map((col) => (
                    <td key={col.key} className="px-4 py-3.5">
                      <div className="h-3 w-2/3 animate-pulse rounded bg-[var(--ltzy-surface-2)]" />
                    </td>
                  ))}
                </tr>
              ))
            : rows.map((row, index) => (
                <tr
                  key={rowKey(row)}
                  className="border-b border-[var(--ltzy-border)]/60 transition-colors last:border-0 hover:bg-[var(--ltzy-surface-2)]/50"
                >
                  {columns.map((col) => (
                    <td
                      key={col.key}
                      className={cn(
                        'px-4 py-3 align-middle',
                        col.align === 'right' && 'text-right',
                        col.align === 'center' && 'text-center',
                      )}
                    >
                      {col.render(row, index)}
                    </td>
                  ))}
                </tr>
              ))}
        </tbody>
      </table>
    </div>
  );
}
