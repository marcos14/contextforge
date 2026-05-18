import { useMemo } from "react";

export function usePagination<T>(items: T[], page: number, pageSize: number) {
  return useMemo(() => {
    const total = items.length;
    const totalPages = Math.max(1, Math.ceil(total / pageSize));
    const safePage = Math.min(Math.max(1, page), totalPages);
    const start = (safePage - 1) * pageSize;
    const end = start + pageSize;
    return {
      total,
      totalPages,
      page: safePage,
      pageItems: items.slice(start, end),
      start,
      end: Math.min(end, total),
    };
  }, [items, page, pageSize]);
}

export function Pagination({
  page,
  totalPages,
  total,
  start,
  end,
  pageSize,
  onPageChange,
  onPageSizeChange,
  pageSizeOptions = [10, 25, 50, 100],
}: {
  page: number;
  totalPages: number;
  total: number;
  start: number;
  end: number;
  pageSize: number;
  onPageChange: (p: number) => void;
  onPageSizeChange?: (n: number) => void;
  pageSizeOptions?: number[];
}) {
  const canPrev = page > 1;
  const canNext = page < totalPages;
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 p-2 border-t border-border text-xs">
      <div className="text-muted-foreground">
        {total === 0 ? "0 itens" : `${start + 1}–${end} de ${total}`}
      </div>
      <div className="flex items-center gap-2">
        {onPageSizeChange && (
          <label className="flex items-center gap-1 text-muted-foreground">
            <span>Por página:</span>
            <select
              className="border border-border rounded px-1 py-0.5 bg-white"
              value={pageSize}
              onChange={(e) => onPageSizeChange(parseInt(e.target.value, 10))}
            >
              {pageSizeOptions.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
        )}
        <div className="flex items-center gap-1">
          <button
            type="button"
            className="px-2 py-1 border border-border rounded bg-white hover:bg-muted disabled:opacity-40"
            onClick={() => onPageChange(1)}
            disabled={!canPrev}
            title="Primeira página"
          >
            «
          </button>
          <button
            type="button"
            className="px-2 py-1 border border-border rounded bg-white hover:bg-muted disabled:opacity-40"
            onClick={() => onPageChange(page - 1)}
            disabled={!canPrev}
            title="Página anterior"
          >
            ‹
          </button>
          <span className="px-2 whitespace-nowrap">
            Página <strong>{page}</strong> de {totalPages}
          </span>
          <button
            type="button"
            className="px-2 py-1 border border-border rounded bg-white hover:bg-muted disabled:opacity-40"
            onClick={() => onPageChange(page + 1)}
            disabled={!canNext}
            title="Próxima página"
          >
            ›
          </button>
          <button
            type="button"
            className="px-2 py-1 border border-border rounded bg-white hover:bg-muted disabled:opacity-40"
            onClick={() => onPageChange(totalPages)}
            disabled={!canNext}
            title="Última página"
          >
            »
          </button>
        </div>
      </div>
    </div>
  );
}
