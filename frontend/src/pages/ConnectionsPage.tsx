import { keepPreviousData, useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api } from "../auth";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { Pagination } from "../components/Pagination";
import { fmtDate, truncate } from "../utils/format";

type ConnPage = { items: any[]; total: number; page: number; page_size: number };

const TYPE_VARIANT: Record<string, "info" | "success" | "warning" | "default" | "muted"> = {
  pg: "info",
  mysql: "info",
  mssql: "info",
  oracle: "warning",
  mongo: "success",
  firebird: "warning",
  rest: "muted",
};

const CONFIG_EXAMPLES: Record<string, { label: string; description: string; config: any }> = {
  pg: {
    label: "PostgreSQL",
    description: "DSN no formato libpq/URL. Suporta sslmode, search_path, etc.",
    config: {
      dsn: "postgres://user:password@host:5432/dbname?sslmode=disable",
    },
  },
  mysql: {
    label: "MySQL / MariaDB",
    description: "DSN no formato do driver go-sql-driver/mysql.",
    config: {
      dsn: "user:password@tcp(host:3306)/dbname?parseTime=true&charset=utf8mb4",
    },
  },
  mssql: {
    label: "SQL Server",
    description: "DSN no formato sqlserver:// (go-mssqldb).",
    config: {
      dsn: "sqlserver://user:password@host:1433?database=dbname&encrypt=disable",
    },
  },
  oracle: {
    label: "Oracle",
    description:
      "Requer build com -tags oracle e Oracle Instant Client (imagem oracle). DSN no formato godror.",
    config: {
      dsn: 'user/password@//host:1521/SERVICE_NAME',
    },
  },
  mongo: {
    label: "MongoDB",
    description: "URI de conexão padrão do MongoDB e database default.",
    config: {
      uri: "mongodb://user:password@host:27017/?authSource=admin",
      database: "mydb",
    },
  },
  firebird: {
    label: "Firebird 5",
    description: "DSN no formato user:password@host:port/path/to/db.fdb.",
    config: {
      dsn: "SYSDBA:masterkey@host:3050/var/lib/firebird/data/mydb.fdb",
    },
  },
  rest: {
    label: "REST API",
    description:
      "base_url, headers fixos e auth. type pode ser: none | bearer | api_key | basic.",
    config: {
      base_url: "https://api.example.com",
      headers: { "Accept": "application/json" },
      auth: {
        type: "bearer",
        token: "SEU_TOKEN_AQUI",
      },
    },
  },
};

// Must mirror the backend regex in handlers_connections.go::connectionNameRe.
const CONNECTION_NAME_RE = /^[a-z0-9_-]{1,64}$/;

export function ConnectionsPage() {
  const qc = useQueryClient();
  const emptyForm = { name: "", type: "pg", description: "", config: '{"dsn":"postgres://..."}' };
  const [form, setForm] = useState(emptyForm);
  const [editingId, setEditingId] = useState<string | null>(null);
  // Tracked separately so we can grandfather legacy names: if the user is
  // editing and didn't touch the name, skip client-side validation.
  const [originalName, setOriginalName] = useState<string | null>(null);
  const [showExamples, setShowExamples] = useState(false);
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);
  const q = useQuery({
    queryKey: ["conns", "page", { page, pageSize, q: debouncedSearch }],
    queryFn: () =>
      api<ConnPage>(
        `/api/connections?page=${page}&page_size=${pageSize}&q=${encodeURIComponent(debouncedSearch)}`
      ),
    placeholderData: keepPreviousData,
  });
  const items = q.data?.items ?? [];
  const total = q.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const start = total === 0 ? 0 : (page - 1) * pageSize;
  const end = start + items.length;
  const resetForm = () => { setEditingId(null); setOriginalName(null); setForm(emptyForm); };
  const nameChanged = originalName === null || form.name !== originalName;
  const nameInvalid = nameChanged && form.name !== "" && !CONNECTION_NAME_RE.test(form.name);
  const save = useMutation({
    mutationFn: () => {
      const body = JSON.stringify({ ...form, config: JSON.parse(form.config) });
      return editingId
        ? api(`/api/connections/${editingId}`, { method: "PUT", body })
        : api("/api/connections", { method: "POST", body });
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["conns"] }); resetForm(); },
  });
  const startEdit = async (id: string) => {
    try {
      const c = await api<any>(`/api/connections/${id}`);
      setEditingId(id);
      setOriginalName(c.name);
      setForm({
        name: c.name,
        type: c.type,
        description: c.description ?? "",
        config: JSON.stringify(c.config, null, 2),
      });
      window.scrollTo({ top: 0, behavior: "smooth" });
    } catch (e) {
      alert("Erro ao carregar conexão: " + (e as Error).message);
    }
  };

  useEffect(() => {
    if (page > totalPages) setPage(totalPages);
  }, [totalPages, page]);
  useEffect(() => {
    setPage(1);
  }, [debouncedSearch, pageSize]);

  return (
    <div className="space-y-4">
      <h2 className="text-2xl font-bold">Connections</h2>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (nameInvalid) return;
          save.mutate();
        }}
        className="grid grid-cols-1 sm:grid-cols-2 gap-3 p-4 border border-border rounded bg-white"
      >
        <div className="sm:col-span-2 text-sm font-medium">
          {editingId ? `Editando conexão ${form.name}` : "Nova conexão"}
        </div>
        <div>
          <input
            className={
              "w-full border rounded px-3 py-2 " +
              (nameInvalid ? "border-red-500" : "border-border")
            }
            placeholder="Name (a-z, 0-9, _ e -)"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
          {nameInvalid && (
            <div className="text-xs text-red-600 mt-1">
              Use apenas letras minúsculas, dígitos, sublinhado (_) e hífen (-), até 64 caracteres.
            </div>
          )}
        </div>
        <select className="border border-border rounded px-3 py-2"
                value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })}>
          {["pg","mysql","mssql","oracle","mongo","firebird","rest"].map((t) => <option key={t}>{t}</option>)}
        </select>
        <input className="sm:col-span-2 border border-border rounded px-3 py-2" placeholder="Description"
               value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
        <textarea className="sm:col-span-2 border border-border rounded px-3 py-2 font-mono text-sm h-24"
                  placeholder="JSON config (encrypted at rest)"
                  value={form.config} onChange={(e) => setForm({ ...form, config: e.target.value })} />
        <button
          className="bg-primary text-white rounded py-2 disabled:opacity-50"
          disabled={save.isPending || nameInvalid}
        >
          {save.isPending ? "Saving..." : editingId ? "Update" : "Create"}
        </button>
        <button type="button" className="border border-border rounded py-2"
                onClick={resetForm} disabled={save.isPending}>
          {editingId ? "Cancelar edição" : "Limpar"}
        </button>
        {save.error && <div className="sm:col-span-2 text-sm text-red-600">{(save.error as Error).message}</div>}
      </form>

      <div className="border border-border rounded bg-white">
        <button
          type="button"
          onClick={() => setShowExamples((v) => !v)}
          className="w-full flex items-center justify-between px-4 py-2 text-left font-medium hover:bg-muted"
        >
          <span>Exemplos de JSON de configuração por banco</span>
          <span className="text-muted-foreground text-sm">{showExamples ? "▲" : "▼"}</span>
        </button>
        {showExamples && (
          <div className="border-t border-border p-4 space-y-4">
            <p className="text-sm text-muted-foreground">
              Clique em <em>Usar este exemplo</em> para preencher o formulário acima.
              Os valores são apenas modelos — substitua host, usuário, senha e
              demais parâmetros pelos da sua conexão.
            </p>
            {Object.entries(CONFIG_EXAMPLES).map(([key, ex]) => (
              <div key={key} className="border border-border rounded">
                <div className="flex items-center justify-between px-3 py-2 bg-muted">
                  <div>
                    <div className="font-semibold">
                      {ex.label} <span className="text-xs text-muted-foreground">({key})</span>
                    </div>
                    <div className="text-xs text-muted-foreground">{ex.description}</div>
                  </div>
                  <div className="space-x-2">
                    <button
                      type="button"
                      className="text-xs px-2 py-1 border border-border rounded bg-white hover:bg-muted"
                      onClick={() => navigator.clipboard.writeText(JSON.stringify(ex.config, null, 2))}
                    >
                      Copiar
                    </button>
                    <button
                      type="button"
                      className="text-xs px-2 py-1 border border-border rounded bg-primary text-white hover:opacity-90"
                      onClick={() =>
                        setForm((f) => ({
                          ...f,
                          type: key,
                          config: JSON.stringify(ex.config, null, 2),
                        }))
                      }
                    >
                      Usar este exemplo
                    </button>
                  </div>
                </div>
                <pre className="text-xs font-mono p-3 overflow-x-auto bg-white">
{JSON.stringify(ex.config, null, 2)}
                </pre>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="border border-border rounded bg-white overflow-hidden">
        <div className="flex items-center justify-between gap-2 p-2 border-b border-border">
          <input
            className="border border-border rounded px-2 py-1 text-sm w-full max-w-xs"
            placeholder="Buscar por nome, tipo ou descrição..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <span className="text-xs text-muted-foreground whitespace-nowrap">
            {q.data ? `${total} conexão(ões)` : ""}
          </span>
        </div>
        <div className="overflow-x-auto">
        <table className="w-full text-sm min-w-[760px]">
          <thead className="bg-muted">
            <tr>
              <th className="text-left p-2">Nome</th>
              <th className="text-left p-2">Tipo</th>
              <th className="text-left p-2">Descrição</th>
              <th className="text-left p-2 whitespace-nowrap">Criado em</th>
              <th className="text-right p-2">Ações</th>
            </tr>
          </thead>
          <tbody>
            {items.map((c) => (
            <tr key={c.id} className="border-t border-border">
              <td className="p-2 font-medium">{c.name}</td>
              <td className="p-2">
                <Badge variant={TYPE_VARIANT[c.type] || "default"}>{c.type}</Badge>
              </td>
              <td className="p-2 max-w-[320px] text-slate-600" title={c.description || ""}>
                {truncate(c.description, 80) || <span className="text-muted-foreground">—</span>}
              </td>
              <td className="p-2 whitespace-nowrap text-xs text-slate-600" title={fmtDate(c.created_at)}>
                {fmtDate(c.created_at, { short: true })}
              </td>
              <td className="p-2 text-right space-x-2 whitespace-nowrap">
                <button className="text-blue-600 text-xs hover:underline" onClick={async () => {
                  try {
                    const res = await api(`/api/connections/${c.id}/test`, { method: "POST" });
                    alert("OK: " + JSON.stringify(res));
                  } catch (e) {
                    alert("Erro: " + (e as Error).message);
                  }
                }}>Testar</button>
                <button className="text-emerald-700 text-xs hover:underline" onClick={() => startEdit(c.id)}>Editar</button>
                <button className="text-red-600 text-xs hover:underline" onClick={async () => {
                  if (!confirm(`Excluir a conexão "${c.name}"?`)) return;
                  try {
                    await api(`/api/connections/${c.id}`, { method: "DELETE" });
                    if (editingId === c.id) resetForm();
                    qc.invalidateQueries({ queryKey: ["conns"] });
                  } catch (e) {
                    alert("Erro: " + (e as Error).message);
                  }
                }}>Excluir</button>
              </td>
            </tr>
              ))}
          </tbody>
        </table>
        </div>
        {total > 0 && (
          <Pagination
            page={page}
            totalPages={totalPages}
            total={total}
            start={start}
            end={end}
            pageSize={pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        )}
        {q.data && total === 0 && !debouncedSearch && (
          <EmptyState title="Nenhuma conexão cadastrada" description="Crie sua primeira conexão usando o formulário acima." />
        )}
        {q.data && total === 0 && debouncedSearch && (
          <EmptyState title="Nenhum resultado" description={`Nenhuma conexão corresponde a "${debouncedSearch}".`} />
        )}
      </div>
    </div>
  );
}
