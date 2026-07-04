import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../auth";
import { Badge, type BadgeVariant } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { Pagination } from "../components/Pagination";
import { CodeEditor, type SqlSchema } from "../components/CodeEditor";

// ============================================================================
// Query Studio (Fase 1d — MVP UI + refine loop)
//
// A dedicated workspace to shape a performant SQL query for use in ANY external
// system (reports, ETL, dashboards, app code) — it does NOT create an MCP tool.
// Three columns (see PLANO §6.2): schema tree · chat/requirement · SQL +
// performance. Consumes /api/query-studio/{chat,explain,preview}.
//
// Refine loop: the last EXPLAIN plan is fed back into the next /chat call so the
// assistant can optimise using the real execution plan.
// ============================================================================

type Conn = { id: string; name: string; type: string };
type Column = { name: string; type: string; nullable?: boolean };
type TableInfo = { schema?: string; name: string; columns?: Column[] };

// Structured assistant proposal (mirrors llm.QueryStudioOutput JSON tags).
type ChatResp = {
  reply: string;
  query?: string;
  explanation?: string;
  suggested_indexes?: string[];
  performance_notes?: string[];
  assumptions?: string[];
};

type ChatMsg = {
  role: "user" | "assistant";
  content: string;
  query?: string;
  explanation?: string;
  suggested_indexes?: string[];
  performance_notes?: string[];
  assumptions?: string[];
};

type ExplainResp = {
  dialect: string;
  plan: string;
  format: string;
  analyze: boolean;
};

type PreviewResp = {
  columns: string[];
  rows: Record<string, any>[];
  count: number;
};

// Saved session history (Fase 2b). The list endpoint returns a light shape; the
// detail endpoint additionally carries chat_log and last_explain.
type SessionListItem = {
  id: string;
  connection_id: string;
  title: string;
  query_text: string;
  created_at: string;
  updated_at: string;
};
type SessionDetail = SessionListItem & {
  chat_log: ChatMsg[] | null;
  last_explain: ExplainResp | null;
};
type SessionPage = {
  items: SessionListItem[];
  total: number;
  page: number;
  page_size: number;
};

// Dialects for which the backend implements a runnable Explainer capability.
// Others (e.g. firebird → ErrUnsupported, oracle → disabled stub, mongo/rest)
// return 422 and the buttons are disabled — the 422 stays the safe fallback if
// this heuristic and the backend ever diverge.
const EXPLAIN_KINDS = new Set(["pg", "postgres", "postgresql", "mysql", "mssql"]);

// Payload handed to ToolsPage (via router state) by "Promover a Tool". The Tools
// form reads it from location.state and seeds a new query tool draft. Keep the
// field names aligned with ToolsPage's form (kind/connection_id/query_text/...).
export type PromoteToolState = {
  kind: "query";
  connection_id?: string;
  query_text: string;
  title?: string;
  description?: string;
};

export function QueryStudioPage() {
  const navigate = useNavigate();
  const conns = useQuery({ queryKey: ["conns"], queryFn: () => api<Conn[]>("/api/connections") });

  const [connId, setConnId] = useState("");
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [chat, setChat] = useState<ChatMsg[]>([]);
  const [chatInput, setChatInput] = useState("");
  const [query, setQuery] = useState("");
  const [explanation, setExplanation] = useState("");
  const [suggestedIndexes, setSuggestedIndexes] = useState<string[]>([]);
  const [performanceNotes, setPerformanceNotes] = useState<string[]>([]);
  const [assumptions, setAssumptions] = useState<string[]>([]);
  // pendingExplain holds the last EXPLAIN plan until it is consumed by the next
  // /chat turn (the refine loop). Cleared right after it is sent to the LLM.
  const [pendingExplain, setPendingExplain] = useState("");
  const [explainResult, setExplainResult] = useState<ExplainResp | null>(null);
  const [previewResult, setPreviewResult] = useState<PreviewResp | null>(null);
  // Plan comparison (Fase 3b): a baseline plan is snapshotted from the current
  // EXPLAIN result; after the user applies a suggested index in their DB (the
  // module never runs DDL) and re-runs EXPLAIN, the "Comparação" tab shows the
  // baseline vs. the current plan side by side with the estimated cost/rows delta.
  const [baselinePlan, setBaselinePlan] = useState<ExplainResp | null>(null);
  const [baselineNote, setBaselineNote] = useState("");
  const [tableSearch, setTableSearch] = useState("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [copied, setCopied] = useState(false);
  const [rightTab, setRightTab] = useState<"plan" | "preview" | "compare">("plan");

  // ---- Session history (Fase 2b) ----
  const qc = useQueryClient();
  const [currentSessionId, setCurrentSessionId] = useState<string | null>(null);
  const [showHistory, setShowHistory] = useState(false);
  const [histPage, setHistPage] = useState(1);
  const [histPageSize, setHistPageSize] = useState(10);
  const [histSearch, setHistSearch] = useState("");

  const chatEnd = useRef<HTMLDivElement | null>(null);
  const chatInputRef = useRef<HTMLTextAreaElement | null>(null);

  const selectedConn = useMemo(
    () => conns.data?.find((c) => c.id === connId) || null,
    [conns.data, connId],
  );
  const explainSupported = !!selectedConn && EXPLAIN_KINDS.has(selectedConn.type.toLowerCase());

  useEffect(() => {
    chatEnd.current?.scrollIntoView({ behavior: "smooth" });
  }, [chat]);

  // Reset schema/validation state when the connection changes.
  useEffect(() => {
    setTables([]);
    setExplainResult(null);
    setPreviewResult(null);
    setPendingExplain("");
    setBaselinePlan(null);
    setBaselineNote("");
  }, [connId]);

  // SQL autocomplete schema derived from the introspected tables (same shape as
  // ToolsPage): maps "schema.table" and bare "table" to their column names.
  const sqlSchema = useMemo<SqlSchema>(() => {
    const out: SqlSchema = {};
    for (const t of tables ?? []) {
      const cols = (t.columns || []).map((c) => String(c.name));
      const qualified = t.schema ? `${t.schema}.${t.name}` : String(t.name);
      out[qualified] = cols;
      if (t.schema) out[String(t.name)] = cols;
    }
    return out;
  }, [tables]);

  const filteredTables = useMemo(() => {
    const q = tableSearch.trim().toLowerCase();
    if (!q) return tables;
    return tables.filter((t) => {
      const qualified = (t.schema ? `${t.schema}.${t.name}` : t.name).toLowerCase();
      return qualified.includes(q);
    });
  }, [tables, tableSearch]);

  const introspect = useMutation({
    mutationFn: () => api<TableInfo[]>(`/api/connections/${connId}/introspect`),
    onSuccess: (data) => {
      setTables(data || []);
      setChat((c) => [
        ...c,
        {
          role: "assistant",
          content: `Schema carregado: ${data?.length ?? 0} tabela(s)/coleção(ões) disponíveis.`,
        },
      ]);
    },
  });

  const applyProposal = (res: ChatResp) => {
    if (res.query) setQuery(res.query);
    if (res.explanation !== undefined) setExplanation(res.explanation || "");
    setSuggestedIndexes(res.suggested_indexes || []);
    setPerformanceNotes(res.performance_notes || []);
    setAssumptions(res.assumptions || []);
  };

  const send = useMutation({
    mutationFn: async (userText: string): Promise<ChatResp> => {
      const history = [...chat, { role: "user", content: userText } as ChatMsg].map((m) => ({
        role: m.role,
        content: m.content,
      }));
      return api<ChatResp>("/api/query-studio/chat", {
        method: "POST",
        body: JSON.stringify({
          connection_id: connId || undefined,
          tables,
          messages: history,
          current_query: query || undefined,
          explain_result: pendingExplain || undefined,
        }),
      });
    },
    onSuccess: (res) => {
      // The plan was fed into this turn; drop it so it isn't resent verbatim.
      if (pendingExplain) setPendingExplain("");
      setChat((c) => [
        ...c,
        {
          role: "assistant",
          content: res.reply,
          query: res.query,
          explanation: res.explanation,
          suggested_indexes: res.suggested_indexes,
          performance_notes: res.performance_notes,
          assumptions: res.assumptions,
        },
      ]);
      // Auto-apply the latest proposal to the SQL workspace so the refine loop
      // stays fluid; the user can still edit the query freely afterwards.
      if (res.query || res.explanation) applyProposal(res);
    },
  });

  const submitChat = (e?: React.FormEvent) => {
    e?.preventDefault();
    const text = chatInput.trim();
    if (!text || send.isPending) return;
    setChat((c) => [...c, { role: "user", content: text }]);
    setChatInput("");
    send.mutate(text);
  };

  const explain = useMutation({
    mutationFn: async (analyze: boolean): Promise<ExplainResp> => {
      return api<ExplainResp>("/api/query-studio/explain", {
        method: "POST",
        body: JSON.stringify({ connection_id: connId, query, analyze }),
      });
    },
    onSuccess: (res) => {
      setExplainResult(res);
      setPendingExplain(res.plan);
      setRightTab("plan");
    },
  });

  const preview = useMutation({
    mutationFn: async (): Promise<PreviewResp> => {
      return api<PreviewResp>("/api/query-studio/preview", {
        method: "POST",
        body: JSON.stringify({ connection_id: connId, query }),
      });
    },
    onSuccess: (res) => {
      setPreviewResult(res);
      setRightTab("preview");
    },
  });

  // ---- Session history queries/mutations ----
  const sessions = useQuery({
    queryKey: ["query-sessions", histPage, histPageSize, histSearch],
    queryFn: () =>
      api<SessionPage>(
        `/api/query-studio/sessions?page=${histPage}&page_size=${histPageSize}` +
          (histSearch ? `&q=${encodeURIComponent(histSearch)}` : ""),
      ),
    enabled: showHistory,
  });

  const saveSession = useMutation({
    mutationFn: async (title: string): Promise<SessionDetail> => {
      return api<SessionDetail>("/api/query-studio/sessions", {
        method: "POST",
        body: JSON.stringify({
          id: currentSessionId || undefined,
          connection_id: connId,
          title,
          query_text: query,
          chat_log: chat,
          last_explain: explainResult || undefined,
        }),
      });
    },
    onSuccess: (res) => {
      setCurrentSessionId(res.id);
      qc.invalidateQueries({ queryKey: ["query-sessions"] });
    },
  });

  const deleteSession = useMutation({
    mutationFn: (id: string) =>
      api<void>(`/api/query-studio/sessions/${id}`, { method: "DELETE" }),
    onSuccess: (_res, id) => {
      if (id === currentSessionId) setCurrentSessionId(null);
      qc.invalidateQueries({ queryKey: ["query-sessions"] });
    },
  });

  const loadSession = useMutation({
    mutationFn: (id: string) => api<SessionDetail>(`/api/query-studio/sessions/${id}`),
    onSuccess: (s) => {
      setConnId(s.connection_id);
      setQuery(s.query_text || "");
      const log = Array.isArray(s.chat_log) ? s.chat_log : [];
      setChat(log);
      setExplainResult(s.last_explain || null);
      setPendingExplain("");
      setPreviewResult(null);
      setBaselinePlan(null);
      setBaselineNote("");
      // Restore the SQL/perf panels from the last assistant proposal, if any.
      const lastProposal = [...log].reverse().find((m) => m.role === "assistant" && m.query);
      if (lastProposal) {
        applyProposal({
          reply: lastProposal.content,
          query: lastProposal.query,
          explanation: lastProposal.explanation,
          suggested_indexes: lastProposal.suggested_indexes,
          performance_notes: lastProposal.performance_notes,
          assumptions: lastProposal.assumptions,
        });
        if (s.query_text) setQuery(s.query_text);
      } else {
        setExplanation("");
        setSuggestedIndexes([]);
        setPerformanceNotes([]);
        setAssumptions([]);
      }
      setCurrentSessionId(s.id);
      setShowHistory(false);
    },
  });

  const onSaveSession = () => {
    if (!connId) {
      window.alert("Selecione uma conexão antes de salvar a sessão.");
      return;
    }
    const title = window.prompt(
      "Título da sessão:",
      query.slice(0, 60) || "Sessão de consulta",
    );
    if (title == null) return;
    const trimmed = title.trim();
    if (!trimmed) return;
    saveSession.mutate(trimmed);
  };

  const runExplain = (analyze: boolean) => {
    if (!query.trim()) return;
    if (!connId) return;
    if (analyze) {
      const ok = window.confirm(
        "EXPLAIN ANALYZE executa a query de verdade no banco (com timeout curto). " +
          "Deseja continuar?",
      );
      if (!ok) return;
    }
    explain.mutate(analyze);
  };

  // Snapshot the current EXPLAIN result as the comparison baseline (Fase 3b).
  // The user then applies a suggested index in their DB and re-runs EXPLAIN; the
  // "Comparação" tab contrasts this baseline with the fresh plan.
  const captureBaseline = () => {
    if (!explainResult) return;
    setBaselinePlan(explainResult);
    // If a single index was suggested, pre-label the baseline with it; otherwise
    // leave the note empty for the user to fill via the compare tab's selector.
    setBaselineNote(suggestedIndexes.length === 1 ? suggestedIndexes[0] : "");
  };

  const clearBaseline = () => {
    setBaselinePlan(null);
    setBaselineNote("");
    if (rightTab === "compare") setRightTab("plan");
  };

  const copyQuery = async () => {
    if (!query) return;
    try {
      await navigator.clipboard.writeText(query);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be unavailable (e.g. non-secure context); ignore silently.
    }
  };

  const exportSql = () => {
    if (!query) return;
    const header = explanation ? `-- ${explanation.replace(/\n/g, "\n-- ")}\n\n` : "";
    const blob = new Blob([header + query + "\n"], { type: "text/sql;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "query-studio.sql";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  // Bridge to the Tools module (Fase 3a): open the tool-creation flow with the
  // current query/explanation pre-filled. We navigate to /tools carrying a
  // PromoteToolState in the router state; ToolsPage seeds its existing form from
  // it. Nothing here creates a tool — the user finishes the flow in ToolsPage.
  const promoteToTool = () => {
    if (!query.trim()) return;
    const state: PromoteToolState = {
      kind: "query",
      connection_id: connId || undefined,
      query_text: query,
      description: explanation || undefined,
    };
    navigate("/tools", { state: { promote: state } });
  };

  const insertRef = (ref: string) => {
    setChatInput((prev) => (prev ? `${prev} ${ref}` : ref));
    chatInputRef.current?.focus();
  };

  const toggleTable = (key: string) => setExpanded((p) => ({ ...p, [key]: !p[key] }));

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div>
          <h2 className="text-2xl font-bold">Query Studio</h2>
          <p className="text-sm text-muted-foreground">
            Estúdio de consultas: descreva a necessidade e receba SQL performático
            para usar em qualquer sistema.
          </p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <button
            type="button"
            className="px-3 py-2 border border-border rounded text-sm hover:bg-muted disabled:opacity-50"
            onClick={onSaveSession}
            disabled={!connId || saveSession.isPending}
            title="Salvar a sessão atual (query + chat + último plano)"
          >
            {saveSession.isPending ? "Salvando..." : currentSessionId ? "Salvar" : "Salvar sessão"}
          </button>
          <button
            type="button"
            className="px-3 py-2 border border-border rounded text-sm hover:bg-muted"
            onClick={() => setShowHistory(true)}
            title="Sessões salvas"
          >
            Histórico
          </button>
          <select
            className="border border-border rounded px-3 py-2 min-w-[220px]"
            value={connId}
            onChange={(e) => setConnId(e.target.value)}
          >
            <option value="">— selecione a conexão —</option>
            {conns.data?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name} ({c.type})
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-12 gap-4">
        {/* ============== Coluna 1: Schema ============== */}
        <div className="lg:col-span-3 border border-border rounded bg-white flex flex-col h-[720px]">
          <div className="flex items-center justify-between border-b border-border px-3 py-2">
            <div className="font-semibold text-sm">Schema</div>
            <button
              type="button"
              className="text-xs px-2 py-1 border border-border rounded hover:bg-muted disabled:opacity-50"
              onClick={() => introspect.mutate()}
              disabled={!connId || introspect.isPending}
              title="Carrega tabelas e colunas da conexão"
            >
              {introspect.isPending ? "..." : "Carregar"}
            </button>
          </div>
          {tables.length > 0 && (
            <div className="px-3 py-2 border-b border-border">
              <input
                className="w-full border border-border rounded px-2 py-1 text-xs"
                placeholder="Filtrar tabelas..."
                value={tableSearch}
                onChange={(e) => setTableSearch(e.target.value)}
              />
            </div>
          )}
          <div className="flex-1 overflow-y-auto p-2 text-sm">
            {tables.length === 0 ? (
              <div className="p-2 text-xs text-muted-foreground">
                {connId
                  ? "Clique em Carregar para explorar tabelas, colunas e tipos."
                  : "Selecione uma conexão."}
              </div>
            ) : (
              <ul className="space-y-0.5">
                {filteredTables.map((t) => {
                  const qualified = t.schema ? `${t.schema}.${t.name}` : t.name;
                  const isOpen = !!expanded[qualified];
                  return (
                    <li key={qualified}>
                      <div className="flex items-center gap-1">
                        <button
                          type="button"
                          className="text-muted-foreground hover:text-slate-900 w-4 text-xs"
                          onClick={() => toggleTable(qualified)}
                          aria-label={isOpen ? "Recolher" : "Expandir"}
                        >
                          {isOpen ? "▾" : "▸"}
                        </button>
                        <button
                          type="button"
                          className="flex-1 text-left truncate font-mono text-xs hover:text-primary"
                          onClick={() => insertRef(qualified)}
                          title="Inserir referência no chat"
                        >
                          {qualified}
                        </button>
                      </div>
                      {isOpen && (
                        <ul className="pl-6 py-0.5 space-y-0.5">
                          {(t.columns || []).map((c) => (
                            <li key={c.name}>
                              <button
                                type="button"
                                className="text-left w-full truncate text-[11px] font-mono text-slate-600 hover:text-primary"
                                onClick={() => insertRef(`${qualified}.${c.name}`)}
                                title="Inserir referência no chat"
                              >
                                {c.name}{" "}
                                <span className="text-slate-400">
                                  {c.type}
                                  {c.nullable ? "?" : ""}
                                </span>
                              </button>
                            </li>
                          ))}
                          {(t.columns || []).length === 0 && (
                            <li className="text-[11px] text-muted-foreground">sem colunas</li>
                          )}
                        </ul>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
            {introspect.error && (
              <div className="m-2 bg-red-50 border border-red-200 text-red-700 rounded p-2 text-xs">
                {(introspect.error as Error).message}
              </div>
            )}
          </div>
        </div>

        {/* ============== Coluna 2: Chat / Requisito ============== */}
        <div className="lg:col-span-4 border border-border rounded bg-white flex flex-col h-[720px]">
          <div className="flex items-center justify-between border-b border-border px-3 py-2">
            <div className="font-semibold text-sm">Requisito</div>
            <button
              type="button"
              className="text-xs px-2 py-1 border border-border rounded hover:bg-muted disabled:opacity-50"
              onClick={() => setChat([])}
              disabled={chat.length === 0}
            >
              Limpar
            </button>
          </div>
          <div className="flex-1 overflow-y-auto p-3 space-y-3">
            {chat.length === 0 && (
              <div className="text-sm text-muted-foreground">
                Descreva em linguagem natural o que você precisa (ex.:{" "}
                <em>"faturamento por cliente no último trimestre"</em>). O assistente
                propõe uma query performática, explica o que ela faz e sugere índices.
                Carregue o schema antes para respostas mais precisas.
              </div>
            )}
            {chat.map((m, i) => (
              <div
                key={i}
                className={
                  m.role === "user"
                    ? "ml-6 bg-primary/10 rounded p-3 text-sm whitespace-pre-wrap"
                    : "mr-6 bg-muted rounded p-3 text-sm whitespace-pre-wrap"
                }
              >
                <div className="text-xs font-semibold text-muted-foreground mb-1">
                  {m.role === "user" ? "Você" : "Assistente"}
                </div>
                <div>{m.content}</div>
                {m.assumptions && m.assumptions.length > 0 && (
                  <div className="mt-2 text-xs">
                    <div className="font-semibold mb-1">Premissas:</div>
                    <ul className="list-disc pl-5 space-y-0.5">
                      {m.assumptions.map((a, j) => (
                        <li key={j}>{a}</li>
                      ))}
                    </ul>
                  </div>
                )}
                {m.query && (
                  <div className="mt-2 space-y-2">
                    <pre className="font-mono text-xs bg-white border border-border rounded p-2 overflow-x-auto whitespace-pre-wrap">
{m.query}
                    </pre>
                    <button
                      type="button"
                      className="text-xs px-2 py-1 bg-primary text-white rounded hover:opacity-90"
                      onClick={() =>
                        applyProposal({
                          reply: m.content,
                          query: m.query,
                          explanation: m.explanation,
                          suggested_indexes: m.suggested_indexes,
                          performance_notes: m.performance_notes,
                          assumptions: m.assumptions,
                        })
                      }
                    >
                      Usar esta query
                    </button>
                  </div>
                )}
              </div>
            ))}
            {send.isPending && (
              <div className="mr-6 bg-muted rounded p-3 text-sm text-muted-foreground">
                Pensando...
              </div>
            )}
            {send.error && (
              <div className="mr-6 bg-red-50 border border-red-200 text-red-700 rounded p-3 text-sm">
                Erro: {(send.error as Error).message}
              </div>
            )}
            <div ref={chatEnd} />
          </div>
          {pendingExplain && (
            <div className="border-t border-border px-3 py-1.5 text-xs bg-amber-50 text-amber-800">
              Plano de execução será enviado ao assistente na próxima mensagem (refino).
            </div>
          )}
          <form onSubmit={submitChat} className="border-t border-border p-2 flex gap-2">
            <textarea
              ref={chatInputRef}
              className="flex-1 border border-border rounded px-2 py-1.5 text-sm resize-none"
              rows={2}
              placeholder="Descreva a consulta que você precisa..."
              value={chatInput}
              onChange={(e) => setChatInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  submitChat();
                }
              }}
            />
            <button
              type="submit"
              className="px-3 bg-primary text-white rounded text-sm disabled:opacity-50"
              disabled={send.isPending || !chatInput.trim()}
            >
              Enviar
            </button>
          </form>
        </div>

        {/* ============== Coluna 3: SQL + Performance ============== */}
        <div className="lg:col-span-5 border border-border rounded bg-white flex flex-col h-[720px]">
          <div className="flex items-center justify-between border-b border-border px-3 py-2">
            <div className="font-semibold text-sm">SQL & Performance</div>
            <div className="flex items-center gap-1.5">
              <button
                type="button"
                className="text-xs px-2 py-1 border border-border rounded hover:bg-muted disabled:opacity-50"
                onClick={copyQuery}
                disabled={!query}
              >
                {copied ? "Copiado!" : "Copiar"}
              </button>
              <button
                type="button"
                className="text-xs px-2 py-1 border border-border rounded hover:bg-muted disabled:opacity-50"
                onClick={exportSql}
                disabled={!query}
              >
                Exportar .sql
              </button>
              <button
                type="button"
                className="text-xs px-2 py-1 border border-primary bg-primary/10 text-primary rounded hover:bg-primary/20 disabled:opacity-50"
                onClick={promoteToTool}
                disabled={!query.trim()}
                title="Abre o cadastro de Tools com esta query já preenchida"
              >
                Promover a Tool
              </button>
            </div>
          </div>
          <div className="flex-1 overflow-y-auto p-3 space-y-3">
            <CodeEditor
              value={query}
              onChange={setQuery}
              language="sql"
              height="12rem"
              label="SQL"
              sqlSchema={sqlSchema}
            />

            <QueryMarkers sql={query} />

            <div className="flex items-center gap-1.5 flex-wrap">
              <button
                type="button"
                className="text-xs px-2 py-1 border border-border rounded hover:bg-muted disabled:opacity-50"
                onClick={() => runExplain(false)}
                disabled={!query.trim() || !connId || !explainSupported || explain.isPending}
                title={
                  explainSupported
                    ? "Plano de execução estimado (não executa a query)"
                    : "EXPLAIN não suportado para este tipo de conexão"
                }
              >
                {explain.isPending ? "..." : "EXPLAIN"}
              </button>
              <button
                type="button"
                className="text-xs px-2 py-1 border border-amber-300 bg-amber-50 text-amber-800 rounded hover:bg-amber-100 disabled:opacity-50"
                onClick={() => runExplain(true)}
                disabled={!query.trim() || !connId || !explainSupported || explain.isPending}
                title="Executa a query de verdade (com timeout curto). Requer confirmação."
              >
                EXPLAIN ANALYZE
              </button>
              <button
                type="button"
                className="text-xs px-2 py-1 border border-border rounded hover:bg-muted disabled:opacity-50"
                onClick={() => preview.mutate()}
                disabled={!query.trim() || !connId || preview.isPending}
                title="Amostra de linhas (LIMIT imposto pelo servidor)"
              >
                {preview.isPending ? "..." : "Preview"}
              </button>
              {!explainSupported && connId && (
                <Badge variant="muted">EXPLAIN indisponível ({selectedConn?.type})</Badge>
              )}
            </div>

            {explanation && (
              <div>
                <div className="text-xs font-semibold text-muted-foreground mb-1">Explicação</div>
                <div className="text-sm whitespace-pre-wrap bg-muted/40 rounded p-2">
                  {explanation}
                </div>
              </div>
            )}

            {suggestedIndexes.length > 0 && (
              <div>
                <div className="text-xs font-semibold text-muted-foreground mb-1">
                  Índices sugeridos <span className="font-normal">(texto — não executados)</span>
                </div>
                <ul className="space-y-1">
                  {suggestedIndexes.map((idx, i) => (
                    <li
                      key={i}
                      className="font-mono text-xs bg-white border border-border rounded p-2 overflow-x-auto whitespace-pre-wrap"
                    >
                      {idx}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <PerformanceNotesPanel notes={performanceNotes} assumptions={assumptions} />

            {/* --- Plano / Preview / Comparação --- */}
            {(explainResult || previewResult || explain.error || preview.error || baselinePlan) && (
              <div className="border border-border rounded">
                <div className="flex items-center border-b border-border text-xs">
                  <button
                    type="button"
                    className={
                      "px-3 py-1.5 " +
                      (rightTab === "plan" ? "font-semibold border-b-2 border-primary" : "text-muted-foreground")
                    }
                    onClick={() => setRightTab("plan")}
                  >
                    Plano de execução
                  </button>
                  <button
                    type="button"
                    className={
                      "px-3 py-1.5 " +
                      (rightTab === "preview" ? "font-semibold border-b-2 border-primary" : "text-muted-foreground")
                    }
                    onClick={() => setRightTab("preview")}
                  >
                    Preview
                  </button>
                  {baselinePlan && (
                    <button
                      type="button"
                      className={
                        "px-3 py-1.5 flex items-center gap-1 " +
                        (rightTab === "compare" ? "font-semibold border-b-2 border-primary" : "text-muted-foreground")
                      }
                      onClick={() => setRightTab("compare")}
                    >
                      Comparação
                      <Badge variant="info">baseline</Badge>
                    </button>
                  )}
                </div>
                <div className="p-2">
                  {rightTab === "plan" &&
                    (explain.error ? (
                      <div className="bg-red-50 border border-red-200 text-red-700 rounded p-2 text-xs">
                        {(explain.error as Error).message}
                      </div>
                    ) : explainResult ? (
                      <div className="space-y-1">
                        <div className="flex items-center gap-2 text-xs flex-wrap">
                          <Badge variant="info">{explainResult.dialect}</Badge>
                          {explainResult.analyze && <Badge variant="warning">ANALYZE (executado)</Badge>}
                          <span className="text-muted-foreground">{explainResult.format}</span>
                          <button
                            type="button"
                            className="ml-auto text-xs px-2 py-0.5 border border-border rounded hover:bg-muted"
                            onClick={captureBaseline}
                            title="Guardar este plano como baseline para comparar depois de aplicar um índice sugerido"
                          >
                            {baselinePlan ? "Redefinir baseline" : "Definir como baseline"}
                          </button>
                        </div>
                        {baselinePlan && (
                          <div className="text-[11px] text-muted-foreground">
                            Baseline guardado. Aplique um índice sugerido no banco, rode EXPLAIN
                            novamente e abra a aba <strong>Comparação</strong>.
                          </div>
                        )}
                        <pre className="font-mono text-[11px] bg-muted/40 rounded p-2 overflow-x-auto whitespace-pre-wrap max-h-72 overflow-y-auto">
{explainResult.plan}
                        </pre>
                      </div>
                    ) : (
                      <div className="text-xs text-muted-foreground p-1">
                        Rode EXPLAIN para ver o plano.
                      </div>
                    ))}
                  {rightTab === "preview" &&
                    (preview.error ? (
                      <div className="bg-red-50 border border-red-200 text-red-700 rounded p-2 text-xs">
                        {(preview.error as Error).message}
                      </div>
                    ) : previewResult ? (
                      previewResult.rows.length === 0 ? (
                        <div className="text-xs text-muted-foreground p-1">Nenhuma linha.</div>
                      ) : (
                        <div className="overflow-x-auto">
                          <div className="text-[11px] text-muted-foreground mb-1">
                            {previewResult.count} linha(s) (amostra limitada pelo servidor)
                          </div>
                          <table className="text-xs border-collapse w-full">
                            <thead>
                              <tr>
                                {previewResult.columns.map((c) => (
                                  <th
                                    key={c}
                                    className="border border-border px-2 py-1 text-left font-semibold bg-muted/40"
                                  >
                                    {c}
                                  </th>
                                ))}
                              </tr>
                            </thead>
                            <tbody>
                              {previewResult.rows.map((row, ri) => (
                                <tr key={ri}>
                                  {previewResult.columns.map((c) => (
                                    <td key={c} className="border border-border px-2 py-1 font-mono">
                                      {formatCell(row[c])}
                                    </td>
                                  ))}
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      )
                    ) : (
                      <div className="text-xs text-muted-foreground p-1">
                        Rode Preview para ver uma amostra.
                      </div>
                    ))}
                  {rightTab === "compare" && (
                    <PlanComparison
                      baseline={baselinePlan}
                      current={explainResult}
                      note={baselineNote}
                      indexes={suggestedIndexes}
                      onNote={setBaselineNote}
                      onClear={clearBaseline}
                    />
                  )}
                </div>
              </div>
            )}

            {!query && !chat.length && (
              <EmptyState
                title="Nenhuma query ainda"
                description="Descreva o requisito no chat ao lado. A query proposta aparece aqui, pronta para validar (EXPLAIN/Preview), copiar ou exportar."
              />
            )}
          </div>
        </div>
      </div>

      {showHistory && (
        <SessionHistory
          data={sessions.data}
          isLoading={sessions.isLoading}
          error={sessions.error as Error | null}
          search={histSearch}
          onSearch={(v) => {
            setHistSearch(v);
            setHistPage(1);
          }}
          page={histPage}
          pageSize={histPageSize}
          onPageChange={setHistPage}
          onPageSizeChange={(n) => {
            setHistPageSize(n);
            setHistPage(1);
          }}
          conns={conns.data || []}
          currentSessionId={currentSessionId}
          loadingId={loadSession.isPending ? (loadSession.variables as string) : null}
          onLoad={(id) => loadSession.mutate(id)}
          onDelete={(id) => {
            if (window.confirm("Excluir esta sessão salva?")) deleteSession.mutate(id);
          }}
          onClose={() => setShowHistory(false)}
        />
      )}
    </div>
  );
}

// SessionHistory is the saved-session drawer (Fase 2b): a searchable, paginated
// list of the current user's sessions with load/delete actions. Pagination is
// server-side; the Pagination component's derived props are computed from total.
function SessionHistory({
  data,
  isLoading,
  error,
  search,
  onSearch,
  page,
  pageSize,
  onPageChange,
  onPageSizeChange,
  conns,
  currentSessionId,
  loadingId,
  onLoad,
  onDelete,
  onClose,
}: {
  data?: SessionPage;
  isLoading: boolean;
  error: Error | null;
  search: string;
  onSearch: (v: string) => void;
  page: number;
  pageSize: number;
  onPageChange: (p: number) => void;
  onPageSizeChange: (n: number) => void;
  conns: Conn[];
  currentSessionId: string | null;
  loadingId: string | null;
  onLoad: (id: string) => void;
  onDelete: (id: string) => void;
  onClose: () => void;
}) {
  const items = data?.items || [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const start = (page - 1) * pageSize;
  const end = start + items.length;
  const connName = (id: string) => conns.find((c) => c.id === id)?.name || id.slice(0, 8);

  return (
    <div
      className="fixed inset-0 z-40 bg-black/30 flex items-start justify-center p-4"
      onClick={onClose}
    >
      <div
        className="bg-white rounded shadow-lg border border-border w-full max-w-2xl mt-10 flex flex-col max-h-[80vh]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <div className="font-semibold">Histórico de sessões</div>
          <button
            type="button"
            className="text-sm px-2 py-1 border border-border rounded hover:bg-muted"
            onClick={onClose}
          >
            Fechar
          </button>
        </div>
        <div className="px-4 py-2 border-b border-border">
          <input
            className="w-full border border-border rounded px-2 py-1.5 text-sm"
            placeholder="Buscar por título..."
            value={search}
            onChange={(e) => onSearch(e.target.value)}
          />
        </div>
        <div className="flex-1 overflow-y-auto">
          {isLoading ? (
            <div className="p-4 text-sm text-muted-foreground">Carregando...</div>
          ) : error ? (
            <div className="m-4 bg-red-50 border border-red-200 text-red-700 rounded p-2 text-sm">
              {error.message}
            </div>
          ) : items.length === 0 ? (
            <EmptyState
              title="Nenhuma sessão salva"
              description="Salve a sessão atual para reabri-la depois."
            />
          ) : (
            <ul className="divide-y divide-border">
              {items.map((s) => (
                <li key={s.id} className="flex items-center gap-3 px-4 py-2">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-medium truncate">{s.title}</span>
                      {s.id === currentSessionId && <Badge variant="info">atual</Badge>}
                    </div>
                    <div className="text-xs text-muted-foreground truncate">
                      {connName(s.connection_id)} ·{" "}
                      {new Date(s.updated_at).toLocaleString("pt-BR")}
                    </div>
                  </div>
                  <button
                    type="button"
                    className="text-xs px-2 py-1 bg-primary text-white rounded hover:opacity-90 disabled:opacity-50"
                    onClick={() => onLoad(s.id)}
                    disabled={loadingId === s.id}
                  >
                    {loadingId === s.id ? "..." : "Carregar"}
                  </button>
                  <button
                    type="button"
                    className="text-xs px-2 py-1 border border-red-300 text-red-700 rounded hover:bg-red-50"
                    onClick={() => onDelete(s.id)}
                  >
                    Excluir
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
        <Pagination
          page={page}
          totalPages={totalPages}
          total={total}
          start={start}
          end={end}
          pageSize={pageSize}
          onPageChange={onPageChange}
          onPageSizeChange={onPageSizeChange}
        />
      </div>
    </div>
  );
}

// ============================================================================
// Anti-pattern detection (Fase 3c)
//
// Two complementary signals are surfaced in the UI, both as pure functions so
// they can be exercised in isolation:
//  1. classifyNote(text): maps a free-text performance note from the LLM
//     (performance_notes) to a known anti-pattern category with a severity and
//     Badge variant, so the notes panel shows *what kind* of issue it is with a
//     colour accent instead of a flat bullet list.
//  2. detectQueryAntiPatterns(sql): a light, false-positive-averse scan of the
//     current SQL text for a few unmistakable anti-patterns (SELECT *, OFFSET,
//     leading-wildcard LIKE, NOT IN), shown as markers right below the editor.
// ============================================================================

export type AntiPatternSeverity = "danger" | "warning" | "info";

const SEVERITY_VARIANT: Record<AntiPatternSeverity, BadgeVariant> = {
  danger: "danger",
  warning: "warning",
  info: "info",
};

// Left-border accent + soft tint per severity for the note cards.
const SEVERITY_ACCENT: Record<AntiPatternSeverity, string> = {
  danger: "border-l-red-400 bg-red-50/50",
  warning: "border-l-amber-400 bg-amber-50/50",
  info: "border-l-blue-300 bg-blue-50/40",
};

// Ordered most-severe-first: the first matching category wins. Patterns accept
// both PT (LLM output language) and EN wording, since the model may mix terms.
const NOTE_CATEGORIES: { label: string; severity: AntiPatternSeverity; test: RegExp }[] = [
  { label: "SELECT *", severity: "danger", test: /select\s*\*|todas as colunas|all columns/i },
  {
    label: "Varredura completa",
    severity: "danger",
    test: /full\s*scan|seq(uential)?\s*scan|table scan|varredura completa|sem\s+where|without\s+where|falta[a-z ]*where|no\s+where/i,
  },
  {
    label: "Produto cartesiano",
    severity: "danger",
    test: /cartesian|cartesiano|cross\s*join|produto cartesiano/i,
  },
  {
    label: "Função em coluna",
    severity: "warning",
    test: /fun[cç][aã]o[a-z ]*(coluna|indexad)|function[a-z ]*(column|index)|sargable|quebra[a-z ]*[ií]ndice|invalida[a-z ]*[ií]ndice/i,
  },
  {
    label: "OFFSET alto",
    severity: "warning",
    test: /offset|keyset|pagina[cç][aã]o|pagination/i,
  },
  {
    label: "Subconsulta correlacionada",
    severity: "warning",
    test: /correlacionad|correlated/i,
  },
  {
    label: "IN (subquery)",
    severity: "warning",
    test: /in\s*\([^)]*select|prefira\s+exists|use\s+exists|em vez de\s+in|instead of\s+in/i,
  },
  {
    label: "LIKE '%…'",
    severity: "warning",
    test: /like\s+'?%|leading\s+wildcard|curinga[a-z ]*(in[ií]cio|esquerda)/i,
  },
  {
    label: "Cast implícito",
    severity: "warning",
    test: /cast impl[ií]cito|implicit (cast|conversion)|convers[aã]o[a-z ]*tipo|type mismatch/i,
  },
  {
    label: "Índice ausente",
    severity: "info",
    test: /[ií]ndice[a-z ]*(ausente|faltando|inexistente|n[aã]o[a-z ]*(existe|usad))|missing index|index[a-z ]*not\s+used|no index/i,
  },
  {
    label: "Ordenação custosa",
    severity: "info",
    test: /order by|\bsort\b|ordena[cç][aã]o|classifica[cç][aã]o/i,
  },
];

// classifyNote assigns a performance note to an anti-pattern category. Anything
// unrecognised falls back to a neutral "Nota" (info) so it still renders.
export function classifyNote(text: string): { label: string; severity: AntiPatternSeverity } {
  const t = text || "";
  for (const c of NOTE_CATEGORIES) {
    if (c.test.test(t)) return { label: c.label, severity: c.severity };
  }
  return { label: "Nota", severity: "info" };
}

export type QueryMarker = {
  key: string;
  label: string;
  severity: AntiPatternSeverity;
  detail: string;
};

// detectQueryAntiPatterns scans the SQL text for a small set of high-confidence
// anti-patterns. Kept deliberately conservative (few, unambiguous regexes) to
// avoid false positives that would erode trust in the markers.
export function detectQueryAntiPatterns(sql: string): QueryMarker[] {
  const markers: QueryMarker[] = [];
  if (!sql || !sql.trim()) return markers;
  if (/select\s+\*/i.test(sql)) {
    markers.push({
      key: "select-star",
      label: "SELECT *",
      severity: "danger",
      detail: "Liste apenas as colunas necessárias em vez de SELECT *.",
    });
  }
  if (/\boffset\s+\d+/i.test(sql)) {
    markers.push({
      key: "offset",
      label: "OFFSET",
      severity: "warning",
      detail:
        "OFFSET alto lê e descarta linhas; prefira paginação por keyset (WHERE coluna > último_valor).",
    });
  }
  if (/\blike\s+'%/i.test(sql)) {
    markers.push({
      key: "leading-wildcard",
      label: "LIKE '%…'",
      severity: "warning",
      detail: "Curinga à esquerda impede o uso de índice B-tree na coluna.",
    });
  }
  if (/\bnot\s+in\s*\(/i.test(sql)) {
    markers.push({
      key: "not-in",
      label: "NOT IN",
      severity: "warning",
      detail: "NOT IN com subconsulta/NULLs é traiçoeiro e lento; considere NOT EXISTS.",
    });
  }
  return markers;
}

// QueryMarkers renders the SQL-derived anti-pattern badges just under the editor.
function QueryMarkers({ sql }: { sql: string }) {
  const markers = detectQueryAntiPatterns(sql);
  if (markers.length === 0) return null;
  return (
    <div className="flex items-center gap-1.5 flex-wrap">
      <span className="text-[11px] text-muted-foreground">Detectado na query:</span>
      {markers.map((m) => (
        <Badge key={m.key} variant={SEVERITY_VARIANT[m.severity]} title={m.detail}>
          ⚠ {m.label}
        </Badge>
      ))}
    </div>
  );
}

// PerformanceNotesPanel highlights the assistant's performance_notes as colour-
// coded cards (by classified anti-pattern) and lists the assumptions below.
function PerformanceNotesPanel({
  notes,
  assumptions,
}: {
  notes: string[];
  assumptions: string[];
}) {
  if (notes.length === 0 && assumptions.length === 0) return null;
  return (
    <div className="space-y-3">
      {notes.length > 0 && (
        <div>
          <div className="text-xs font-semibold text-muted-foreground mb-1">
            Notas de performance <span className="font-normal">(anti-padrões destacados)</span>
          </div>
          <ul className="space-y-1.5">
            {notes.map((n, i) => {
              const { label, severity } = classifyNote(n);
              return (
                <li
                  key={i}
                  className={
                    "flex items-start gap-2 rounded border-l-4 px-2 py-1.5 text-sm " +
                    SEVERITY_ACCENT[severity]
                  }
                >
                  <Badge variant={SEVERITY_VARIANT[severity]} className="mt-0.5 shrink-0">
                    {label}
                  </Badge>
                  <span className="flex-1">{n}</span>
                </li>
              );
            })}
          </ul>
        </div>
      )}
      {assumptions.length > 0 && (
        <div>
          <div className="text-xs font-semibold text-muted-foreground mb-1">Premissas</div>
          <ul className="space-y-1">
            {assumptions.map((a, i) => (
              <li key={i} className="flex items-start gap-2 text-sm">
                <Badge variant="muted" className="mt-0.5 shrink-0">
                  premissa
                </Badge>
                <span className="flex-1">{a}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

// formatCell renders a preview cell value as a compact string. Objects/arrays
// are JSON-stringified; null/undefined show as an em dash.
function formatCell(v: any): string {
  if (v === null || v === undefined) return "—";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

// ============================================================================
// Plan comparison (Fase 3b)
// ============================================================================

// Comparable metrics extracted best-effort from an EXPLAIN plan. Only JSON plans
// (Postgres `FORMAT JSON`, MySQL `FORMAT=JSON`) expose machine-readable numbers;
// text/xml plans (MySQL ANALYZE / MSSQL) return null and are shown side by side
// without a numeric delta.
type PlanMetrics = {
  totalCost?: number;
  planRows?: number;
  actualTime?: number;
  actualRows?: number;
};

function num(v: any): number | undefined {
  if (v === null || v === undefined) return undefined;
  const n = typeof v === "number" ? v : parseFloat(String(v));
  return Number.isFinite(n) ? n : undefined;
}

export function extractPlanMetrics(plan: string, format: string): PlanMetrics | null {
  if (format !== "json" || !plan) return null;
  let parsed: any;
  try {
    parsed = JSON.parse(plan);
  } catch {
    return null;
  }
  // Postgres: array (or object) whose root carries a "Plan" node.
  const pgRoot = Array.isArray(parsed) ? parsed[0] : parsed;
  if (pgRoot && pgRoot.Plan) {
    const p = pgRoot.Plan;
    return {
      totalCost: num(p["Total Cost"]),
      planRows: num(p["Plan Rows"]),
      actualTime: num(p["Actual Total Time"]),
      actualRows: num(p["Actual Rows"]),
    };
  }
  // MySQL FORMAT=JSON: query_block.cost_info.query_cost.
  const qb = parsed?.query_block;
  if (qb) {
    const table = qb.table || {};
    return {
      totalCost: num(qb?.cost_info?.query_cost),
      planRows: num(table?.rows_examined_per_scan ?? table?.rows_produced_per_join),
    };
  }
  return null;
}

// deltaPct returns the signed percentage change from baseline to current
// (negative = improvement, i.e. lower cost/rows/time). null when incomparable.
export function deltaPct(baseline?: number, current?: number): number | null {
  if (baseline === undefined || current === undefined || baseline === 0) return null;
  return ((current - baseline) / baseline) * 100;
}

function fmtNum(v?: number): string {
  if (v === undefined) return "—";
  return v.toLocaleString("pt-BR", { maximumFractionDigits: 2 });
}

// PlanComparison contrasts a baseline plan with the current plan (Fase 3b),
// showing the estimated cost/rows/time delta and both plan texts side by side.
function PlanComparison({
  baseline,
  current,
  note,
  indexes,
  onNote,
  onClear,
}: {
  baseline: ExplainResp | null;
  current: ExplainResp | null;
  note: string;
  indexes: string[];
  onNote: (v: string) => void;
  onClear: () => void;
}) {
  if (!baseline) {
    return (
      <div className="text-xs text-muted-foreground p-1">
        Nenhum baseline definido. Rode EXPLAIN e clique em “Definir como baseline”.
      </div>
    );
  }

  const mb = extractPlanMetrics(baseline.plan, baseline.format);
  const mc = current ? extractPlanMetrics(current.plan, current.format) : null;
  const rows: { label: string; b?: number; c?: number }[] = [
    { label: "Custo estimado", b: mb?.totalCost, c: mc?.totalCost },
    { label: "Linhas estimadas", b: mb?.planRows, c: mc?.planRows },
    { label: "Tempo real (ms)", b: mb?.actualTime, c: mc?.actualTime },
    { label: "Linhas reais", b: mb?.actualRows, c: mc?.actualRows },
  ].filter((r) => r.b !== undefined || r.c !== undefined);

  const sameFormat = !current || baseline.format === current.format;

  return (
    <div className="space-y-3 text-xs">
      <div className="flex items-center gap-2 flex-wrap">
        <Badge variant="muted">baseline</Badge>
        <span className="text-muted-foreground">vs.</span>
        <Badge variant="info">plano atual</Badge>
        <button
          type="button"
          className="ml-auto px-2 py-0.5 border border-border rounded hover:bg-muted"
          onClick={onClear}
        >
          Limpar baseline
        </button>
      </div>

      {/* Which suggested index this comparison evaluates. */}
      <div className="space-y-1">
        <div className="font-semibold text-muted-foreground">Índice avaliado (opcional)</div>
        {indexes.length > 0 ? (
          <select
            className="w-full border border-border rounded px-2 py-1 text-xs font-mono"
            value={note}
            onChange={(e) => onNote(e.target.value)}
          >
            <option value="">— nenhum / anotação livre —</option>
            {indexes.map((idx, i) => (
              <option key={i} value={idx}>
                {idx}
              </option>
            ))}
          </select>
        ) : (
          <input
            className="w-full border border-border rounded px-2 py-1 text-xs font-mono"
            placeholder="Ex.: CREATE INDEX ... (aplicado no banco antes do 2º EXPLAIN)"
            value={note}
            onChange={(e) => onNote(e.target.value)}
          />
        )}
      </div>

      {!current ? (
        <div className="bg-amber-50 border border-amber-200 text-amber-800 rounded p-2">
          Baseline guardado. Aplique o índice no banco, rode <strong>EXPLAIN</strong> novamente
          para gerar o plano atual e compará-los aqui.
        </div>
      ) : (
        <>
          {rows.length > 0 ? (
            <table className="w-full border-collapse">
              <thead>
                <tr>
                  <th className="border border-border px-2 py-1 text-left bg-muted/40">Métrica</th>
                  <th className="border border-border px-2 py-1 text-right bg-muted/40">Baseline</th>
                  <th className="border border-border px-2 py-1 text-right bg-muted/40">Atual</th>
                  <th className="border border-border px-2 py-1 text-right bg-muted/40">Variação</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const d = deltaPct(r.b, r.c);
                  const improved = d !== null && d < 0;
                  const worse = d !== null && d > 0;
                  return (
                    <tr key={r.label}>
                      <td className="border border-border px-2 py-1">{r.label}</td>
                      <td className="border border-border px-2 py-1 text-right font-mono">
                        {fmtNum(r.b)}
                      </td>
                      <td className="border border-border px-2 py-1 text-right font-mono">
                        {fmtNum(r.c)}
                      </td>
                      <td
                        className={
                          "border border-border px-2 py-1 text-right font-mono " +
                          (improved ? "text-green-700" : worse ? "text-red-700" : "text-muted-foreground")
                        }
                      >
                        {d === null ? "—" : `${d > 0 ? "+" : ""}${d.toFixed(1)}%`}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          ) : (
            <div className="text-muted-foreground">
              {sameFormat
                ? "Este formato de plano não expõe métricas numéricas comparáveis; veja os planos lado a lado abaixo."
                : `Formatos diferentes (${baseline.format} vs. ${current.format}) — comparação apenas textual.`}
            </div>
          )}

          <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
            <div>
              <div className="font-semibold text-muted-foreground mb-1">Baseline ({baseline.format})</div>
              <pre className="font-mono text-[11px] bg-muted/40 rounded p-2 overflow-x-auto whitespace-pre-wrap max-h-60 overflow-y-auto">
{baseline.plan}
              </pre>
            </div>
            <div>
              <div className="font-semibold text-muted-foreground mb-1">Atual ({current.format})</div>
              <pre className="font-mono text-[11px] bg-muted/40 rounded p-2 overflow-x-auto whitespace-pre-wrap max-h-60 overflow-y-auto">
{current.plan}
              </pre>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
