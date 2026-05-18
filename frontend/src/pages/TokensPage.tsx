import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Fragment, useMemo, useState } from "react";
import { api } from "../auth";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { fmtDate, fmtRelative } from "../utils/format";

type Tool = { id: string; slug: string; title?: string; group_id: string; status?: string };
type Group = { id: string; name: string; description?: string };
type Grant = { id: string; scope: "tool" | "group"; target_id: string };
type Token = {
  id: string;
  name: string;
  prefix: string;
  rate_limit_per_min: number;
  revoked_at?: string | null;
  created_at?: string | null;
  last_used_at?: string | null;
  expires_at?: string | null;
};

export function TokensPage() {
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: () => api<Token[]>("/api/tokens") });
  const tools = useQuery({ queryKey: ["tools"], queryFn: () => api<Tool[]>("/api/tools") });
  const groups = useQuery({ queryKey: ["groups"], queryFn: () => api<Group[]>("/api/groups") });
  const [name, setName] = useState("");
  const [rate, setRate] = useState(120);
  const [ipAllowlist, setIpAllowlist] = useState("");
  const [created, setCreated] = useState<any>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [tokenSearch, setTokenSearch] = useState("");
  const grants = useQuery({
    queryKey: ["grants", selected],
    queryFn: () => api<Grant[]>(`/api/tokens/${selected}/grants`),
    enabled: !!selected,
  });

  const create = useMutation({
    mutationFn: () => {
      const ips = ipAllowlist
        .split(/[\s,;]+/)
        .map((s) => s.trim())
        .filter(Boolean);
      return api("/api/tokens", {
        method: "POST",
        body: JSON.stringify({ name, rate_limit_per_min: rate, ip_allowlist: ips }),
      });
    },
    onSuccess: (d) => {
      setCreated(d);
      qc.invalidateQueries({ queryKey: ["tokens"] });
      setName("");
      setIpAllowlist("");
    },
  });

  const addGrant = useMutation({
    mutationFn: (g: { scope: "tool" | "group"; target_id: string }) =>
      api(`/api/tokens/${selected}/grants`, { method: "POST", body: JSON.stringify(g) }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["grants", selected], refetchType: "active" }),
  });

  const removeGrant = useMutation({
    mutationFn: (grantId: string) =>
      api(`/api/tokens/${selected}/grants/${grantId}`, { method: "DELETE" }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["grants", selected], refetchType: "active" }),
  });

  // Indexes for lookup.
  const toolsById = useMemo(() => {
    const m = new Map<string, Tool>();
    (tools.data ?? []).forEach((t) => m.set(t.id, t));
    return m;
  }, [tools.data]);
  const groupsById = useMemo(() => {
    const m = new Map<string, Group>();
    (groups.data ?? []).forEach((g) => m.set(g.id, g));
    return m;
  }, [groups.data]);
  const toolsByGroup = useMemo(() => {
    const m = new Map<string, Tool[]>();
    (tools.data ?? []).forEach((t) => {
      const list = m.get(t.group_id) ?? [];
      list.push(t);
      m.set(t.group_id, list);
    });
    return m;
  }, [tools.data]);

  // Split current grants into groups vs individual tools.
  const grantedGroupIds = useMemo(
    () => new Set((grants.data ?? []).filter((g) => g.scope === "group").map((g) => g.target_id)),
    [grants.data],
  );
  const grantedToolIds = useMemo(
    () => new Set((grants.data ?? []).filter((g) => g.scope === "tool").map((g) => g.target_id)),
    [grants.data],
  );
  const groupGrants = useMemo(
    () => (grants.data ?? []).filter((g) => g.scope === "group"),
    [grants.data],
  );
  const toolGrants = useMemo(
    () => (grants.data ?? []).filter((g) => g.scope === "tool"),
    [grants.data],
  );

  // Tool is "covered by group" if its group_id is in grantedGroupIds.
  const isToolCoveredByGroup = (toolId: string) => {
    const t = toolsById.get(toolId);
    return !!t && grantedGroupIds.has(t.group_id);
  };

  // Options for selectors, filtered to avoid overlaps.
  const availableGroups = (groups.data ?? []).filter((g) => !grantedGroupIds.has(g.id));
  const availableTools = (tools.data ?? []).filter(
    (t) => !grantedToolIds.has(t.id) && !grantedGroupIds.has(t.group_id),
  );

  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const toggle = (id: string) => setExpanded((s) => ({ ...s, [id]: !s[id] }));

  const handleAddGroup = async (groupId: string) => {
    if (!groupId) return;
    // Find individual tool grants that would become redundant.
    const groupToolIds = new Set((toolsByGroup.get(groupId) ?? []).map((t) => t.id));
    const redundant = toolGrants.filter((g) => groupToolIds.has(g.target_id));
    if (redundant.length > 0) {
      const ok = window.confirm(
        `Este grupo já contém ${redundant.length} tool(s) que estão concedidas individualmente a este token:\n\n` +
          redundant
            .map((g) => "• " + (toolsById.get(g.target_id)?.slug ?? g.target_id.slice(0, 8)))
            .join("\n") +
          `\n\nDeseja remover essas concessões individuais (recomendado, evita sobreposição) e adicionar o grupo?`,
      );
      if (!ok) return;
      for (const g of redundant) {
        await removeGrant.mutateAsync(g.id);
      }
    }
    await addGrant.mutateAsync({ scope: "group", target_id: groupId });
  };

  const handleAddTool = async (toolId: string) => {
    if (!toolId) return;
    const t = toolsById.get(toolId);
    if (t && grantedGroupIds.has(t.group_id)) {
      const groupName = groupsById.get(t.group_id)?.name ?? "grupo";
      window.alert(
        `Esta tool já é acessível porque o grupo "${groupName}" foi concedido a este token. Nenhuma ação necessária.`,
      );
      return;
    }
    await addGrant.mutateAsync({ scope: "tool", target_id: toolId });
  };

  const revokeToken = useMutation({
    mutationFn: () => api(`/api/tokens/${selected}/revoke`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"], refetchType: "active" }),
  });

  const reactivateToken = useMutation({
    mutationFn: () => api(`/api/tokens/${selected}/reactivate`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"], refetchType: "active" }),
  });

  const selectedToken = tokens.data?.find((t) => t.id === selected);

  return (
    <div className="space-y-4">
      <h2 className="text-2xl font-bold">Tokens (MCP clients)</h2>
      <p className="text-sm text-gray-600">
        Tokens são credenciais usadas por clientes MCP (ex.: Claude, ChatGPT, scripts internos) para chamar suas
        ferramentas. Cada token recebe <b>acessos</b> que definem quais tools/grupos ele pode usar. O segredo só é
        exibido <b>uma única vez</b> no momento da criação — guarde-o em local seguro.
      </p>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
        className="bg-white border border-border p-4 rounded space-y-3"
      >
        <div>
          <label className="block text-xs font-semibold text-gray-600 mb-1">Nome</label>
          <input
            className="w-full border border-border rounded px-3 py-2"
            placeholder="ex.: Claude Web, Bot Financeiro"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <p className="text-xs text-gray-500 mt-1">
            Identificação interna do token — apenas para você reconhecê-lo na listagem.
          </p>
        </div>

        <div>
          <label className="block text-xs font-semibold text-gray-600 mb-1">Rate limit (requisições por minuto)</label>
          <input
            type="number"
            min={1}
            className="w-32 border border-border rounded px-3 py-2"
            value={rate}
            onChange={(e) => setRate(+e.target.value)}
          />
          <p className="text-xs text-gray-500 mt-1">
            Limite máximo de chamadas por minuto para este token. Pode ser sobrescrito por acesso individual.
          </p>
        </div>

        <div>
          <label className="block text-xs font-semibold text-gray-600 mb-1">
            IP allowlist <span className="font-normal text-gray-400">(opcional)</span>
          </label>
          <input
            className="w-full border border-border rounded px-3 py-2 font-mono text-sm"
            placeholder="ex.: 192.168.0.10, 10.0.0.0/24"
            value={ipAllowlist}
            onChange={(e) => setIpAllowlist(e.target.value)}
          />
          <p className="text-xs text-gray-500 mt-1">
            Lista de IPs ou faixas CIDR autorizados, separados por vírgula. Deixe vazio para permitir qualquer origem.
          </p>
        </div>

        <button className="bg-primary text-white rounded px-4 py-2" disabled={create.isPending}>
          {create.isPending ? "Criando…" : "Criar token"}
        </button>
        {create.isError && (
          <div className="text-sm text-red-600">{(create.error as Error)?.message || "Erro ao criar"}</div>
        )}
      </form>

      {created && (
        <div className="bg-yellow-50 border border-yellow-300 p-3 rounded text-sm">
          Copie agora — só será mostrado uma vez:
          <pre className="font-mono mt-1 break-all">{created.token}</pre>
          <button className="text-xs text-blue-600" onClick={() => setCreated(null)}>fechar</button>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="border border-border rounded bg-white">
          <div className="flex items-center justify-between gap-2 p-2 border-b border-border">
            <input
              className="border border-border rounded px-2 py-1 text-sm w-full max-w-xs"
              placeholder="Buscar por nome ou prefixo..."
              value={tokenSearch}
              onChange={(e) => setTokenSearch(e.target.value)}
            />
            <span className="text-xs text-muted-foreground whitespace-nowrap">
              {tokens.data ? `${tokens.data.length} token(s)` : ""}
            </span>
          </div>
          <ul className="divide-y">
            {(tokens.data ?? [])
              .filter((t) => {
                if (!tokenSearch.trim()) return true;
                const s = tokenSearch.toLowerCase();
                return (
                  (t.name || "").toLowerCase().includes(s) ||
                  (t.prefix || "").toLowerCase().includes(s)
                );
              })
              .map((t) => {
                const revoked = !!t.revoked_at;
                const expired = !!t.expires_at && new Date(t.expires_at).getTime() < Date.now();
                const neverUsed = !t.last_used_at;
                return (
            <li
              key={t.id}
              className={
                "p-3 cursor-pointer " +
                (selected === t.id ? "bg-muted " : "") +
                (revoked ? "opacity-60" : "")
              }
              onClick={() => setSelected(t.id)}
            >
              <div className="flex items-center justify-between gap-2">
                <div className="font-medium truncate">{t.name}</div>
                <div className="flex items-center gap-1 shrink-0">
                  {revoked && <Badge variant="danger">Revogado</Badge>}
                  {!revoked && expired && <Badge variant="danger">Expirado</Badge>}
                  {!revoked && !expired && neverUsed && <Badge variant="muted">Nunca usado</Badge>}
                  {!revoked && !expired && !neverUsed && <Badge variant="success">Ativo</Badge>}
                </div>
              </div>
              <div className="text-xs font-mono text-gray-500 mt-1">
                {t.prefix} · {t.rate_limit_per_min}/min
              </div>
              <div className="text-xs text-gray-500 mt-1 flex flex-wrap gap-x-3">
                <span title={fmtDate(t.created_at)}>Criado: {fmtDate(t.created_at, { short: true })}</span>
                <span title={fmtDate(t.last_used_at)}>
                  Último uso: {t.last_used_at ? fmtRelative(t.last_used_at) : "—"}
                </span>
                {t.expires_at && (
                  <span title={fmtDate(t.expires_at)}>Expira: {fmtDate(t.expires_at, { short: true })}</span>
                )}
              </div>
            </li>
                );
              })}
          </ul>
          {tokens.data && tokens.data.length === 0 && (
            <EmptyState title="Nenhum token criado" description="Crie um token usando o formulário acima." />
          )}
        </div>

        {selected && (
          <div className="border border-border rounded bg-white p-3 space-y-4">
            <div className="flex justify-between items-start">
              <div>
                <h3 className="font-semibold">Acessos do token</h3>
                <p className="text-xs text-gray-500 mt-1">
                  Conceda acesso por <b>grupo</b> (todas as tools do grupo de uma vez) ou por <b>tool individual</b>.
                  Tools cobertas por um grupo concedido não precisam ser adicionadas avulsas.
                </p>
              </div>
              {selectedToken && !selectedToken.revoked_at && (
                <button
                  className="text-xs text-red-600 border border-red-300 rounded px-2 py-1 hover:bg-red-50"
                  onClick={() => {
                    if (
                      window.confirm(
                        `Revogar o token "${selectedToken.name}"?\n\nO token deixará imediatamente de funcionar para todos os clientes que o utilizam. Você poderá reativá-lo depois, se necessário.`,
                      )
                    )
                      revokeToken.mutate();
                  }}
                >
                  Revogar token
                </button>
              )}
              {selectedToken && selectedToken.revoked_at && (
                <button
                  className="text-xs text-green-700 border border-green-300 rounded px-2 py-1 hover:bg-green-50"
                  onClick={() => {
                    if (
                      window.confirm(
                        `Reativar o token "${selectedToken.name}"?\n\nO token voltará a funcionar imediatamente com o mesmo segredo e os mesmos acessos que tinha antes da revogação.`,
                      )
                    )
                      reactivateToken.mutate();
                  }}
                >
                  Reativar token
                </button>
              )}
            </div>

            {/* ===== Tabela de acessos ===== */}
            <div className="overflow-x-auto border border-border rounded">
              <table className="w-full text-sm min-w-[560px]">
                <thead className="bg-gray-50 text-xs text-gray-600">
                  <tr>
                    <th className="text-left px-3 py-2 w-8"></th>
                    <th className="text-left px-3 py-2 w-24">Tipo</th>
                    <th className="text-left px-3 py-2">Nome</th>
                    <th className="text-left px-3 py-2">Detalhes</th>
                    <th className="text-right px-3 py-2 w-24">Ações</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {groupGrants.length === 0 && toolGrants.length === 0 && (
                    <tr>
                      <td colSpan={5} className="px-3 py-4 text-center text-xs text-gray-400 italic">
                        Nenhum acesso concedido. Use os seletores abaixo para adicionar grupos ou tools.
                      </td>
                    </tr>
                  )}

                  {/* Linhas de grupos (com sub-linhas das tools) */}
                  {groupGrants.map((g) => {
                    const group = groupsById.get(g.target_id);
                    const groupTools = toolsByGroup.get(g.target_id) ?? [];
                    const isOpen = !!expanded[g.id];
                    return (
                      <Fragment key={g.id}>
                        <tr className="bg-blue-50/40">
                          <td className="px-3 py-2 align-middle">
                            <button
                              className="text-gray-500 hover:text-gray-800 text-xs w-4"
                              onClick={() => toggle(g.id)}
                              title={isOpen ? "Recolher" : "Expandir tools"}
                            >
                              {isOpen ? "▾" : "▸"}
                            </button>
                          </td>
                          <td className="px-3 py-2 align-middle">
                            <span className="inline-block text-[10px] uppercase tracking-wide font-semibold text-blue-700 bg-blue-100 rounded px-1.5 py-0.5">
                              Grupo
                            </span>
                          </td>
                          <td className="px-3 py-2 align-middle font-medium">
                            {group?.name ?? <span className="text-gray-400">{g.target_id.slice(0, 8)}…</span>}
                          </td>
                          <td className="px-3 py-2 align-middle text-xs text-gray-600">
                            {groupTools.length} tool{groupTools.length === 1 ? "" : "s"}
                            {group?.description && <span className="text-gray-400"> · {group.description}</span>}
                          </td>
                          <td className="px-3 py-2 align-middle text-right">
                            <button
                              className="text-red-600 text-xs hover:underline"
                              onClick={() => {
                                if (
                                  window.confirm(
                                    `Revogar acesso ao grupo "${group?.name ?? ""}"?\n\nO token perderá acesso a todas as ${groupTools.length} tool(s) deste grupo. Esta ação é imediata.`,
                                  )
                                )
                                  removeGrant.mutate(g.id);
                              }}
                            >
                              Revogar
                            </button>
                          </td>
                        </tr>
                        {isOpen &&
                          (groupTools.length === 0 ? (
                            <tr key={g.id + "-empty"}>
                              <td className="px-3 py-1"></td>
                              <td colSpan={4} className="px-3 py-1 text-xs italic text-gray-400">
                                Grupo vazio.
                              </td>
                            </tr>
                          ) : (
                            groupTools.map((t) => (
                              <tr key={g.id + "-" + t.id} className="bg-gray-50/60 text-xs">
                                <td className="px-3 py-1"></td>
                                <td className="px-3 py-1 text-gray-400">└ tool</td>
                                <td className="px-3 py-1 font-mono text-gray-700">{t.slug}</td>
                                <td className="px-3 py-1 text-gray-500">
                                  {t.title}
                                  {t.status && t.status !== "active" && (
                                    <span className="ml-1 text-gray-400">({t.status})</span>
                                  )}
                                </td>
                                <td className="px-3 py-1 text-right text-gray-400 italic">via grupo</td>
                              </tr>
                            ))
                          ))}
                      </Fragment>
                    );
                  })}

                  {/* Linhas de tools avulsas */}
                  {toolGrants.map((g) => {
                    const t = toolsById.get(g.target_id);
                    const covered = t && isToolCoveredByGroup(t.id);
                    const groupName = t ? groupsById.get(t.group_id)?.name : undefined;
                    return (
                      <tr key={g.id} className={covered ? "bg-yellow-50" : ""}>
                        <td className="px-3 py-2"></td>
                        <td className="px-3 py-2 align-middle">
                          <span className="inline-block text-[10px] uppercase tracking-wide font-semibold text-green-700 bg-green-100 rounded px-1.5 py-0.5">
                            Tool
                          </span>
                        </td>
                        <td className="px-3 py-2 align-middle font-mono">
                          {t?.slug ?? <span className="text-gray-400">{g.target_id.slice(0, 8)}…</span>}
                        </td>
                        <td className="px-3 py-2 align-middle text-xs text-gray-600">
                          {groupName && <span>grupo: {groupName}</span>}
                          {covered && (
                            <div className="text-yellow-700 mt-0.5">
                              ⚠ redundante — já coberta pelo grupo concedido
                            </div>
                          )}
                        </td>
                        <td className="px-3 py-2 align-middle text-right">
                          <button
                            className="text-red-600 text-xs hover:underline"
                            onClick={() => {
                              if (
                                window.confirm(
                                  `Revogar acesso à tool "${t?.slug ?? g.target_id.slice(0, 8)}"?\n\nO token perderá acesso a esta tool imediatamente.`,
                                )
                              )
                                removeGrant.mutate(g.id);
                            }}
                          >
                            Revogar
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            {/* ===== Adicionar acessos ===== */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label className="block text-xs font-semibold text-gray-600 mb-1">Adicionar grupo</label>
                <select
                  className="w-full border border-border rounded px-2 py-1 text-sm"
                  value=""
                  onChange={(e) => {
                    const v = e.target.value;
                    e.target.value = "";
                    handleAddGroup(v);
                  }}
                  disabled={availableGroups.length === 0}
                >
                  <option value="">
                    {availableGroups.length === 0
                      ? "— todos os grupos já concedidos —"
                      : "+ escolher grupo…"}
                  </option>
                  {availableGroups.map((g) => {
                    const count = (toolsByGroup.get(g.id) ?? []).length;
                    return (
                      <option key={g.id} value={g.id}>
                        {g.name} ({count} tool{count === 1 ? "" : "s"})
                      </option>
                    );
                  })}
                </select>
              </div>
              <div>
                <label className="block text-xs font-semibold text-gray-600 mb-1">Adicionar tool individual</label>
                <select
                  className="w-full border border-border rounded px-2 py-1 text-sm"
                  value=""
                  onChange={(e) => {
                    const v = e.target.value;
                    e.target.value = "";
                    handleAddTool(v);
                  }}
                  disabled={availableTools.length === 0}
                >
                  <option value="">
                    {availableTools.length === 0
                      ? "— nenhuma tool disponível —"
                      : "+ escolher tool…"}
                  </option>
                  {availableTools.map((t) => {
                    const groupName = groupsById.get(t.group_id)?.name ?? "?";
                    return (
                      <option key={t.id} value={t.id}>
                        {t.slug} — {groupName}
                      </option>
                    );
                  })}
                </select>
                <p className="text-xs text-gray-400 mt-1">
                  Tools cujo grupo já foi concedido não aparecem aqui (evita sobreposição).
                </p>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
