import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { useState } from "react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api } from "../auth";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { fmtDate, truncate } from "../utils/format";

type StatusFilter = "all" | "success" | "error";

// Consumption period selector values, mirroring the backend `?period=` param.
type Period = "24h" | "7d" | "30d";

type StatsSummary = {
  total: number;
  errors: number;
  cache_hits: number;
  error_rate: number;
  cache_hit_rate: number;
};

type StatsBucket = {
  bucket: string;
  total: number;
  errors: number;
  cache_hits: number;
};

type StatsByTool = {
  tool_slug: string;
  total: number;
  errors: number;
};

type Stats = {
  period: Period;
  summary: StatsSummary;
  timeseries: StatsBucket[];
  by_tool: StatsByTool[];
};

// Validated chart palette (dataviz skill): blue for executions, red for errors.
const CHART_BLUE = "#2a78d6";
const CHART_RED = "#d03b3b";
const CHART_GRID = "#e1e0d9";
const CHART_AXIS = "#898781";

const PERIODS: { value: Period; label: string }[] = [
  { value: "24h", label: "24h" },
  { value: "7d", label: "7 dias" },
  { value: "30d", label: "30 dias" },
];

function fmtPct(v: number | undefined): string {
  if (v == null || isNaN(v)) return "—";
  return `${(v * 100).toFixed(1)}%`;
}

function fmtInt(v: number | undefined): string {
  if (v == null || isNaN(v)) return "—";
  return v.toLocaleString("pt-BR");
}

// Formats a bucket timestamp for the x-axis: hour of day for 24h, day/month
// for the wider windows.
function bucketLabel(iso: string, period: Period): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  if (period === "24h") {
    return d.toLocaleTimeString("pt-BR", { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleDateString("pt-BR", { day: "2-digit", month: "2-digit" });
}

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

  // Shared consumption period, also reused by later home panels.
  const [period, setPeriod] = useState<Period>("24h");

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
      <ConsumptionSection period={period} onPeriodChange={setPeriod} />
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

// Consumption dashboard: period selector + KPI cards + time series and
// per-tool charts, all driven by GET /api/stats.
function ConsumptionSection({
  period,
  onPeriodChange,
}: {
  period: Period;
  onPeriodChange: (p: Period) => void;
}) {
  const stats = useQuery({
    queryKey: ["stats", period],
    queryFn: () => api<Stats>(`/api/stats?period=${period}`),
    placeholderData: keepPreviousData,
  });

  const summary = stats.data?.summary;
  const timeseries = (stats.data?.timeseries ?? []).map((b) => ({
    ...b,
    label: bucketLabel(b.bucket, period),
  }));
  const byTool = stats.data?.by_tool ?? [];
  const hasData = (summary?.total ?? 0) > 0;

  return (
    <section className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-semibold">Consumo</h3>
        <div className="inline-flex rounded border border-border overflow-hidden text-sm">
          {PERIODS.map((p) => (
            <button
              key={p.value}
              type="button"
              onClick={() => onPeriodChange(p.value)}
              className={
                "px-3 py-1 border-l border-border first:border-l-0 " +
                (period === p.value ? "bg-primary text-white" : "bg-white hover:bg-muted")
              }
              aria-pressed={period === p.value}
            >
              {p.label}
            </button>
          ))}
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <Card title="Execuções" value={fmtInt(summary?.total)} />
        <Card title="Taxa de erro" value={fmtPct(summary?.error_rate)} />
        <Card title="Cache hit" value={fmtPct(summary?.cache_hit_rate)} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="border border-border rounded p-4 bg-white">
          <div className="text-sm font-medium mb-3">Execuções ao longo do tempo</div>
          {!stats.isLoading && !hasData ? (
            <EmptyState title="Sem dados no período" />
          ) : (
            <ResponsiveContainer width="100%" height={260}>
              <AreaChart data={timeseries} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
                <defs>
                  <linearGradient id="execFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={CHART_BLUE} stopOpacity={0.35} />
                    <stop offset="100%" stopColor={CHART_BLUE} stopOpacity={0.02} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke={CHART_GRID} vertical={false} />
                <XAxis
                  dataKey="label"
                  tick={{ fontSize: 11, fill: CHART_AXIS }}
                  stroke={CHART_GRID}
                  minTickGap={16}
                />
                <YAxis
                  allowDecimals={false}
                  tick={{ fontSize: 11, fill: CHART_AXIS }}
                  stroke={CHART_GRID}
                  width={40}
                />
                <Tooltip />
                <Legend />
                <Area
                  type="monotone"
                  name="Execuções"
                  dataKey="total"
                  stroke={CHART_BLUE}
                  strokeWidth={2}
                  fill="url(#execFill)"
                />
                <Line
                  type="monotone"
                  name="Erros"
                  dataKey="errors"
                  stroke={CHART_RED}
                  strokeWidth={2}
                  dot={false}
                />
              </AreaChart>
            </ResponsiveContainer>
          )}
        </div>

        <div className="border border-border rounded p-4 bg-white">
          <div className="text-sm font-medium mb-3">Execuções por tool</div>
          {!stats.isLoading && byTool.length === 0 ? (
            <EmptyState title="Sem dados no período" />
          ) : (
            <ResponsiveContainer width="100%" height={260}>
              <BarChart data={byTool} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
                <CartesianGrid stroke={CHART_GRID} vertical={false} />
                <XAxis
                  dataKey="tool_slug"
                  tick={{ fontSize: 11, fill: CHART_AXIS }}
                  stroke={CHART_GRID}
                  interval={0}
                  angle={-20}
                  textAnchor="end"
                  height={54}
                />
                <YAxis
                  allowDecimals={false}
                  tick={{ fontSize: 11, fill: CHART_AXIS }}
                  stroke={CHART_GRID}
                  width={40}
                />
                <Tooltip />
                <Legend />
                <Bar name="Execuções" dataKey="total" fill={CHART_BLUE} radius={[4, 4, 0, 0]} />
                <Bar name="Erros" dataKey="errors" fill={CHART_RED} radius={[4, 4, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          )}
        </div>
      </div>

      {stats.isError && (
        <div className="text-sm text-red-700">Falha ao carregar métricas de consumo.</div>
      )}
    </section>
  );
}
