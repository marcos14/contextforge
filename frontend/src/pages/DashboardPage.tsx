import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../auth";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { fmtDate, truncate } from "../utils/format";

type StatusFilter = "all" | "success" | "error";

type Execution = {
  occurred_at: string;
  tool_slug: string;
  status: string;
  cache_hit?: boolean;
  duration_ms?: number | null;
  error_message?: string | null;
};

type Page<T> = { items: T[]; total: number; page: number; page_size: number };

export function DashboardPage() {
  const reg = useQuery({ queryKey: ["registry"], queryFn: () => api<any>("/api/registry") });

  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [search, setSearch] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);

  const exec = useQuery({
    queryKey: ["execs", page, pageSize, statusFilter, search],
    queryFn: () => {
      const sp = new URLSearchParams({
        page: String(page),
        page_size: String(pageSize),
      });
      if (statusFilter !== "all") sp.set("status", statusFilter);
      if (search) sp.set("q", search);
      return api<Page<Execution>>(`/api/executions?${sp.toString()}`);
    },
    placeholderData: keepPreviousData,
  });

  const items = exec.data?.items ?? [];
  const total = exec.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const start = (page - 1) * pageSize;

  const onSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setPage(1);
    setSearch(searchInput.trim());
  };

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">Dashboard</h2>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        <Card title="Tools ativas" value={reg.data?.tools_count ?? "—"} />
        <Card title="Tokens ativos" value={reg.data?.tokens ?? "—"} />
        <Card title="Snapshot" value={reg.data?.built_at?.slice(11, 19) ?? "—"} />
      </div>
      <section>
        <div className="flex flex-wrap items-center justify-between gap-2 mb-2">
          <h3 className="font-semibold">Últimas execuções</h3>
          <form className="flex items-center gap-2" onSubmit={onSearchSubmit}>
            <input
              className="border border-border rounded px-2 py-1 text-sm"
              placeholder="Buscar tool ou erro..."
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
            />
            <select
              className="border border-border rounded px-2 py-1 text-sm"
              value={statusFilter}
              onChange={(e) => {
                setPage(1);
                setStatusFilter(e.target.value as StatusFilter);
              }}
            >
              <option value="all">Todos</option>
              <option value="success">Sucesso</option>
              <option value="error">Erro</option>
            </select>
            <button type="submit" className="px-3 py-1 text-sm bg-primary text-white rounded">
              Buscar
            </button>
            {search && (
              <button
                type="button"
                className="px-3 py-1 text-sm hover:bg-muted rounded"
                onClick={() => {
                  setSearchInput("");
                  setSearch("");
                  setPage(1);
                }}
              >
                Limpar
              </button>
            )}
          </form>
        </div>
        <div className="border border-border rounded overflow-x-auto bg-white">
          <table className="w-full text-sm min-w-[860px]">
            <thead className="bg-muted">
              <tr>
                <th className="text-left p-2 whitespace-nowrap">Quando</th>
                <th className="text-left p-2">Tool</th>
                <th className="text-left p-2">Status</th>
                <th className="text-left p-2">Cache</th>
                <th className="text-right p-2">ms</th>
                <th className="text-left p-2">Erro</th>
              </tr>
            </thead>
            <tbody>
              {items.map((e, i) => {
                const s = (e.status || "").toLowerCase();
                const ok = s === "ok" || s === "success" || s === "succeeded";
                const err = e.error_message || "";
                return (
                  <tr key={i} className="border-t border-border">
                    <td
                      className="p-2 whitespace-nowrap text-xs text-slate-600"
                      title={fmtDate(e.occurred_at)}
                    >
                      {fmtDate(e.occurred_at)}
                    </td>
                    <td className="p-2 font-mono">{e.tool_slug}</td>
                    <td className="p-2">
                      <Badge variant={ok ? "success" : "danger"}>{e.status}</Badge>
                    </td>
                    <td className="p-2">{e.cache_hit ? <Badge variant="info">hit</Badge> : ""}</td>
                    <td className="p-2 text-right">{e.duration_ms}</td>
                    <td className="p-2 max-w-[320px] text-red-700 text-xs" title={err}>
                      {truncate(err, 80)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {!exec.isLoading && items.length === 0 && total === 0 && (
            <EmptyState
              title={
                search || statusFilter !== "all"
                  ? "Nenhuma execução corresponde aos filtros"
                  : "Sem execuções recentes"
              }
              description={
                !search && statusFilter === "all"
                  ? "Assim que tools forem chamadas, elas aparecerão aqui."
                  : undefined
              }
            />
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
                  disabled={page <= 1 || exec.isFetching}
                >
                  ←
                </button>
                <span>
                  {page} / {totalPages}
                </span>
                <button
                  className="px-2 py-1 border border-border rounded disabled:opacity-40"
                  onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  disabled={page >= totalPages || exec.isFetching}
                >
                  →
                </button>
              </div>
            </div>
          )}
        </div>
      </section>
    </div>
  );
}

function Card({ title, value }: { title: string; value: any }) {
  return (
    <div className="border border-border rounded p-4 bg-white">
      <div className="text-xs text-gray-500">{title}</div>
      <div className="text-2xl font-bold">{String(value)}</div>
    </div>
  );
}
