import { keepPreviousData, useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { api } from "../auth";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { Pagination } from "../components/Pagination";
import { fmtDate } from "../utils/format";

type Group = {
  id: string;
  name: string;
  description?: string;
  hidden_by_default?: boolean;
  created_at?: string;
};

type GroupPage = { items: Group[]; total: number; page: number; page_size: number };

const emptyForm = { name: "", description: "", hidden_by_default: true };

export function GroupsPage() {
  const qc = useQueryClient();

  const [form, setForm] = useState(emptyForm);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [actionError, setActionError] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  const q = useQuery({
    queryKey: ["groups", "page", { page, pageSize, q: debouncedSearch }],
    queryFn: () =>
      api<GroupPage>(
        `/api/groups?page=${page}&page_size=${pageSize}&q=${encodeURIComponent(debouncedSearch)}`
      ),
    placeholderData: keepPreviousData,
  });
  const tools = useQuery({ queryKey: ["tools"], queryFn: () => api<any[]>("/api/tools") });
  const items = q.data?.items ?? [];
  const total = q.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const start = total === 0 ? 0 : (page - 1) * pageSize;
  const end = start + items.length;

  const resetForm = () => {
    setEditingId(null);
    setForm(emptyForm);
    setActionError(null);
  };

  const save = useMutation({
    mutationFn: () => {
      const body = JSON.stringify({
        name: form.name.trim(),
        description: form.description.trim(),
        hidden_by_default: form.hidden_by_default,
      });
      return editingId
        ? api(`/api/groups/${editingId}`, { method: "PUT", body })
        : api("/api/groups", { method: "POST", body });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups"] });
      resetForm();
    },
    onError: (e) => setActionError((e as Error).message),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api(`/api/groups/${id}`, { method: "DELETE" }),
    onSuccess: (_d, id) => {
      qc.invalidateQueries({ queryKey: ["groups"] });
      if (editingId === id) resetForm();
    },
    onError: (e) => setActionError((e as Error).message),
  });

  const startEdit = (g: Group) => {
    setEditingId(g.id);
    setForm({
      name: g.name,
      description: g.description ?? "",
      hidden_by_default: g.hidden_by_default ?? true,
    });
    setActionError(null);
    window.scrollTo({ top: 0, behavior: "smooth" });
  };

  const toolsCountByGroup = useMemo(() => {
    const map = new Map<string, number>();
    for (const t of tools.data ?? []) {
      const gid = t.group_id || "";
      if (!gid) continue;
      map.set(gid, (map.get(gid) ?? 0) + 1);
    }
    return map;
  }, [tools.data]);

  useEffect(() => {
    if (page > totalPages) setPage(totalPages);
  }, [totalPages, page]);
  useEffect(() => {
    setPage(1);
  }, [debouncedSearch, pageSize]);

  return (
    <div className="space-y-4">
      <h2 className="text-2xl font-bold">Tool Groups</h2>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!form.name.trim()) return;
          save.mutate();
        }}
        className="grid grid-cols-1 sm:grid-cols-2 gap-3 p-4 border border-border rounded bg-white"
      >
        <div className="sm:col-span-2 text-sm font-medium">
          {editingId ? `Editando grupo "${form.name}"` : "Novo grupo"}
        </div>

        <div className="sm:col-span-1">
          <label className="block text-xs font-semibold text-gray-600 mb-1">Nome</label>
          <input
            className="w-full border border-border rounded px-3 py-2"
            placeholder="ex.: Financeiro, Vendas"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            required
          />
        </div>

        <div className="sm:col-span-1">
          <label className="block text-xs font-semibold text-gray-600 mb-1">Visibilidade</label>
          <label className="flex items-center gap-2 h-[42px] px-3 border border-border rounded bg-white text-sm">
            <input
              type="checkbox"
              checked={form.hidden_by_default}
              onChange={(e) => setForm({ ...form, hidden_by_default: e.target.checked })}
            />
            <span>Oculto por padrão</span>
          </label>
          <p className="text-xs text-gray-500 mt-1">
            Quando oculto, as tools deste grupo só aparecem para tokens com acesso explícito.
          </p>
        </div>

        <div className="sm:col-span-2">
          <label className="block text-xs font-semibold text-gray-600 mb-1">Descrição</label>
          <input
            className="w-full border border-border rounded px-3 py-2"
            placeholder="Breve descrição do propósito do grupo"
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
          />
        </div>

        <div className="sm:col-span-2 flex gap-2 items-center">
          <button
            type="submit"
            className="bg-primary text-white rounded px-4 py-2 disabled:opacity-50"
            disabled={save.isPending || !form.name.trim()}
          >
            {save.isPending ? "Salvando..." : editingId ? "Atualizar" : "Adicionar"}
          </button>
          <button
            type="button"
            className="border border-border rounded px-4 py-2"
            onClick={resetForm}
            disabled={save.isPending}
          >
            {editingId ? "Cancelar edição" : "Limpar"}
          </button>
          {(save.error || actionError) && (
            <div className="text-sm text-red-600">
              {(save.error as Error)?.message || actionError}
            </div>
          )}
        </div>
      </form>

      <div className="border border-border rounded bg-white">
        <div className="flex items-center justify-between gap-2 p-2 border-b border-border">
          <input
            className="border border-border rounded px-2 py-1 text-sm w-full max-w-xs"
            placeholder="Buscar grupo..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <span className="text-xs text-muted-foreground whitespace-nowrap">
            {q.data ? `${total} grupo(s)` : ""}
          </span>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm min-w-[720px]">
            <thead className="bg-muted">
              <tr>
                <th className="text-left p-2">Nome</th>
                <th className="text-left p-2">Descrição</th>
                <th className="text-left p-2">Tools</th>
                <th className="text-left p-2">Visibilidade</th>
                <th className="text-left p-2 whitespace-nowrap">Criado em</th>
                <th className="text-right p-2">Ações</th>
              </tr>
            </thead>
            <tbody>
              {items.map((g) => {
                const count = toolsCountByGroup.get(g.id) ?? 0;
                return (
                  <tr
                    key={g.id}
                    className={"border-t border-border " + (editingId === g.id ? "bg-blue-50" : "")}
                  >
                    <td className="p-2 font-medium">{g.name}</td>
                    <td className="p-2 text-slate-600">
                      {g.description || <span className="text-muted-foreground">—</span>}
                    </td>
                    <td className="p-2">
                      <Badge variant="info">{count}</Badge>
                    </td>
                    <td className="p-2">
                      <Badge variant={g.hidden_by_default ? "muted" : "success"}>
                        {g.hidden_by_default ? "oculto" : "visível"}
                      </Badge>
                    </td>
                    <td className="p-2 whitespace-nowrap text-xs text-slate-600" title={fmtDate(g.created_at)}>
                      {fmtDate(g.created_at, { short: true })}
                    </td>
                    <td className="p-2 text-right whitespace-nowrap space-x-3">
                      <button
                        className="text-blue-600 text-xs hover:underline"
                        onClick={() => startEdit(g)}
                      >
                        Editar
                      </button>
                      <button
                        className="text-red-600 text-xs hover:underline disabled:opacity-40 disabled:no-underline"
                        disabled={count > 0 || remove.isPending}
                        title={
                          count > 0
                            ? `Este grupo possui ${count} tool(s). Remova ou mova as tools antes de excluir.`
                            : "Excluir grupo"
                        }
                        onClick={() => {
                          setActionError(null);
                          if (window.confirm(`Excluir o grupo "${g.name}"?`)) {
                            remove.mutate(g.id);
                          }
                        }}
                      >
                        Excluir
                      </button>
                    </td>
                  </tr>
                );
              })}
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
          <EmptyState
            title="Nenhum grupo criado"
            description="Crie um grupo usando o formulário acima."
          />
        )}
        {q.data && total === 0 && debouncedSearch && (
          <EmptyState title="Nenhum grupo corresponde ao filtro" />
        )}
      </div>
    </div>
  );
}
