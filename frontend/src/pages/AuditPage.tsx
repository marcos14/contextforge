import { useQuery } from "@tanstack/react-query";
import { Fragment, useState } from "react";
import { api, getAuth } from "../auth";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { fmtDate } from "../utils/format";

type AuditEntry = {
  id: number;
  occurred_at: string;
  actor_id: string | null;
  actor_email: string | null;
  action: string;
  target: string | null;
  details: any;
};

type Page = {
  items: AuditEntry[];
  total: number;
  page: number;
  page_size: number;
};

export function AuditPage() {
  const auth = getAuth();
  const isAdmin = auth?.role === "admin";
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  const query = useQuery({
    queryKey: ["audit", page, pageSize, search],
    queryFn: () =>
      api<Page>(
        `/api/audit-logs?page=${page}&page_size=${pageSize}` +
          (search ? `&q=${encodeURIComponent(search)}` : ""),
      ),
    enabled: isAdmin,
  });

  if (!isAdmin) {
    return (
      <div className="max-w-xl">
        <h1 className="text-2xl font-bold mb-2">Auditoria</h1>
        <div className="rounded border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
          Esta seção é restrita a administradores.
        </div>
      </div>
    );
  }

  const items = query.data?.items ?? [];
  const total = query.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const start = (page - 1) * pageSize;

  return (
    <div className="max-w-5xl">
      <div className="mb-4">
        <h1 className="text-2xl font-bold">Auditoria</h1>
        <p className="text-sm text-slate-500">
          Histórico de ações administrativas sensíveis.
        </p>
      </div>

      <form
        className="flex gap-2 mb-3"
        onSubmit={(e) => {
          e.preventDefault();
          setPage(1);
          setSearch(q.trim());
        }}
      >
        <input
          placeholder="Buscar por ação ou alvo…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          className="border border-border rounded px-2 py-1 text-sm flex-1"
        />
        <button className="px-3 py-1 text-sm bg-primary text-white rounded" type="submit">
          Buscar
        </button>
        {search && (
          <button
            type="button"
            className="px-3 py-1 text-sm hover:bg-muted rounded"
            onClick={() => {
              setQ("");
              setSearch("");
              setPage(1);
            }}
          >
            Limpar
          </button>
        )}
      </form>

      <div className="border border-border rounded bg-white">
        {query.isLoading ? (
          <div className="p-6 text-sm text-slate-500">Carregando…</div>
        ) : items.length === 0 ? (
          <EmptyState title="Nenhuma entrada de auditoria." />
        ) : (
          <table className="w-full text-sm">
            <thead className="bg-muted text-left">
              <tr>
                <th className="px-3 py-2 w-44">Quando</th>
                <th className="px-3 py-2">Ator</th>
                <th className="px-3 py-2">Ação</th>
                <th className="px-3 py-2">Alvo</th>
                <th className="px-3 py-2 w-16"></th>
              </tr>
            </thead>
            <tbody>
              {items.map((e) => {
                const open = expanded.has(e.id);
                return (
                  <Fragment key={e.id}>
                    <tr className="border-t border-border">
                      <td className="px-3 py-2 text-slate-500 whitespace-nowrap">
                        {fmtDate(e.occurred_at)}
                      </td>
                      <td className="px-3 py-2">
                        {e.actor_email || (
                          <span className="text-slate-400">—</span>
                        )}
                      </td>
                      <td className="px-3 py-2">
                        <Badge variant="info">{e.action}</Badge>
                      </td>
                      <td className="px-3 py-2 font-mono text-xs">
                        {e.target || <span className="text-slate-400">—</span>}
                      </td>
                      <td className="px-3 py-2 text-right">
                        {e.details && (
                          <button
                            className="text-xs px-2 py-1 hover:bg-muted rounded"
                            onClick={() => {
                              setExpanded((prev) => {
                                const next = new Set(prev);
                                if (next.has(e.id)) next.delete(e.id);
                                else next.add(e.id);
                                return next;
                              });
                            }}
                          >
                            {open ? "−" : "+"}
                          </button>
                        )}
                      </td>
                    </tr>
                    {open && e.details && (
                      <tr className="border-t border-border bg-muted/50">
                        <td colSpan={5} className="px-3 py-2">
                          <pre className="text-xs overflow-x-auto">
                            {JSON.stringify(e.details, null, 2)}
                          </pre>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        )}

        {total > 0 && (
          <div className="flex flex-wrap items-center justify-between gap-2 p-2 border-t border-border text-xs">
            <div className="text-slate-500">
              {start + 1}–{Math.min(start + items.length, total)} de {total}
            </div>
            <div className="flex items-center gap-2">
              <label className="flex items-center gap-1 text-slate-500">
                <span>Por página:</span>
                <select
                  className="border border-border rounded px-1 py-0.5 bg-white"
                  value={pageSize}
                  onChange={(e) => {
                    setPageSize(parseInt(e.target.value, 10));
                    setPage(1);
                  }}
                >
                  {[10, 25, 50, 100].map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              </label>
              <button
                className="px-2 py-1 border border-border rounded disabled:opacity-40"
                onClick={() => setPage((p) => Math.max(1, p - 1))}
                disabled={page <= 1}
              >
                ←
              </button>
              <span>
                {page} / {totalPages}
              </span>
              <button
                className="px-2 py-1 border border-border rounded disabled:opacity-40"
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                disabled={page >= totalPages}
              >
                →
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
