import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../auth";
import { Badge } from "../components/Badge";
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

export function QueryStudioPage() {
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
  const [tableSearch, setTableSearch] = useState("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [copied, setCopied] = useState(false);
  const [rightTab, setRightTab] = useState<"plan" | "preview">("plan");

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

            {performanceNotes.length > 0 && (
              <div>
                <div className="text-xs font-semibold text-muted-foreground mb-1">
                  Notas de performance
                </div>
                <ul className="list-disc pl-5 space-y-0.5 text-sm">
                  {performanceNotes.map((n, i) => (
                    <li key={i}>{n}</li>
                  ))}
                </ul>
              </div>
            )}

            {/* --- Plano / Preview --- */}
            {(explainResult || previewResult || explain.error || preview.error) && (
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
                </div>
                <div className="p-2">
                  {rightTab === "plan" &&
                    (explain.error ? (
                      <div className="bg-red-50 border border-red-200 text-red-700 rounded p-2 text-xs">
                        {(explain.error as Error).message}
                      </div>
                    ) : explainResult ? (
                      <div className="space-y-1">
                        <div className="flex items-center gap-2 text-xs">
                          <Badge variant="info">{explainResult.dialect}</Badge>
                          {explainResult.analyze && <Badge variant="warning">ANALYZE (executado)</Badge>}
                          <span className="text-muted-foreground">{explainResult.format}</span>
                        </div>
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

// formatCell renders a preview cell value as a compact string. Objects/arrays
// are JSON-stringified; null/undefined show as an em dash.
function formatCell(v: any): string {
  if (v === null || v === undefined) return "—";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}
