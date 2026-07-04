import { keepPreviousData, useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useRef, useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { api } from "../auth";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { Pagination } from "../components/Pagination";
import { CodeEditor, type SqlSchema } from "../components/CodeEditor";
import { fmtDate } from "../utils/format";
import type { PromoteToolState } from "./QueryStudioPage";

const HELP_DISMISSED_KEY = "mcpb_help_tools_dismissed";

type ParamSpec = {
  name: string;
  type: string;
  items_type?: string;
  description?: string;
  required?: boolean;
};
type ChatMsg = {
  role: "user" | "assistant";
  content: string;
  query?: string;
  params?: ParamSpec[];
  slug?: string;
  title?: string;
  description?: string;
  notes?: string;
  isProposal?: boolean;
};

type ChatResp = {
  reply: string;
  query?: string;
  params?: ParamSpec[];
  slug?: string;
  title?: string;
  description?: string;
  notes?: string;
};

type LastTest = {
  ts: string;
  status: "ok" | "error";
  params?: any;
  columns?: string[];
  sample_rows?: any[];
  row_count?: number;
  error?: string;
};

const INITIAL_FORM = {
  kind: "query" as "query" | "code",
  group_id: "",
  connection_id: "",
  slug: "",
  title: "",
  description: "",
  query_text: "SELECT 1 AS n",
  params_schema: '{"type":"object","properties":{}}',
  row_limit: 1000,
  timeout_ms: 15000,
  cache_ttl_sec: 0,
  cache_per_token: false,
  test_params: {},
  activate: true,
  code_refs: { connection_ids: [] as string[], tool_slugs: [] as string[] },
};

const DEFAULT_CODE_BODY = `// Available globals:
//   params, context.token_id
//   db(connName, sql, paramsObj) -> { rows, columns, count }
//   tools.call(slug, paramsObj) -> { rows, columns, count }
//   fetch(url, opts) -> { status, headers, body, json() }
//   log(...), console.log(...)
return { rows: [{ hello: 'world' }] };
`;

function paramsToSchema(params: ParamSpec[]): string {
  const properties: Record<string, any> = {};
  const required: string[] = [];
  for (const p of params || []) {
    const t = (p.type || "string").toLowerCase();
    if (t === "array") {
      properties[p.name] = {
        type: "array",
        items: { type: (p.items_type || "string").toLowerCase() },
        description: p.description || "",
      };
    } else {
      properties[p.name] = { type: t, description: p.description || "" };
    }
    if (p.required) required.push(p.name);
  }
  const schema: any = { type: "object", properties };
  if (required.length) schema.required = required;
  return JSON.stringify(schema, null, 2);
}

// extractSelectAliases parses the top-level SELECT clause of a SQL string and
// returns the output column names (the alias after AS, or the bare
// identifier). This is a best-effort heuristic used to inform the assistant
// of a query tool's output shape when output_schema is not declared. It is
// NOT a SQL parser: it strips line/block comments, scans up to the matching
// FROM keyword while skipping nested parentheses, and splits on commas at
// depth 0. Returns at most 50 entries.
function extractSelectAliases(sql: string): string[] {
  if (!sql) return [];
  // Strip comments.
  let s = sql.replace(/--[^\n]*/g, " ").replace(/\/\*[\s\S]*?\*\//g, " ");
  const m = /\bselect\b/i.exec(s);
  if (!m) return [];
  s = s.slice(m.index + m[0].length);
  // Optional DISTINCT / TOP etc.
  s = s.replace(/^\s*(distinct|all|top\s+\d+)\s+/i, "");
  // Find the matching FROM at depth 0.
  let depth = 0;
  let end = -1;
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (ch === "(") depth++;
    else if (ch === ")") depth = Math.max(0, depth - 1);
    else if (depth === 0) {
      if (/\bfrom\b/i.test(s.slice(i, i + 5)) && /\W/.test(s[i - 1] || " ")) {
        end = i;
        break;
      }
    }
  }
  const list = end >= 0 ? s.slice(0, end) : s;
  // Split on commas at depth 0.
  const parts: string[] = [];
  let buf = "";
  depth = 0;
  for (const ch of list) {
    if (ch === "(") depth++;
    else if (ch === ")") depth = Math.max(0, depth - 1);
    if (ch === "," && depth === 0) {
      parts.push(buf);
      buf = "";
    } else {
      buf += ch;
    }
  }
  if (buf.trim()) parts.push(buf);
  const out: string[] = [];
  for (const p of parts) {
    const raw = p.trim().replace(/\s+/g, " ");
    if (!raw) continue;
    // AS <alias> (quoted or bare).
    let alias = "";
    const asMatch = raw.match(/\s+as\s+("([^"]+)"|`([^`]+)`|\[([^\]]+)\]|(\w+))\s*$/i);
    if (asMatch) {
      alias = asMatch[2] || asMatch[3] || asMatch[4] || asMatch[5] || "";
    } else {
      // No AS: take the last identifier of a dotted expression (e.g. c.uf -> uf).
      const tail = raw.split(/\s+/).pop() || raw;
      const dotted = tail.split(".").pop() || tail;
      // Strip quotes / brackets.
      alias = dotted.replace(/[`"[\]]/g, "");
    }
    if (alias && /^[A-Za-z_][\w]*$/.test(alias)) out.push(alias);
    if (out.length >= 50) break;
  }
  return out;
}

export function ToolsPage() {
  const qc = useQueryClient();
  const location = useLocation();
  const navigate = useNavigate();
  const tools = useQuery({ queryKey: ["tools"], queryFn: () => api<any[]>("/api/tools") });
  const groups = useQuery({ queryKey: ["groups"], queryFn: () => api<any[]>("/api/groups") });
  const conns = useQuery({ queryKey: ["conns"], queryFn: () => api<any[]>("/api/connections") });

  const [f, setF] = useState(INITIAL_FORM);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [tables, setTables] = useState<any[]>([]);
  const [chat, setChat] = useState<ChatMsg[]>([]);
  const [chatInput, setChatInput] = useState("");
  const [lastTest, setLastTest] = useState<LastTest | null>(null);
  const chatEnd = useRef<HTMLDivElement | null>(null);
  const chatInputRef = useRef<HTMLTextAreaElement | null>(null);
  const [helpOpen, setHelpOpen] = useState<boolean>(() => {
    try {
      return localStorage.getItem(HELP_DISMISSED_KEY) !== "1";
    } catch {
      return true;
    }
  });
  const [tableSearch, setTableSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(tableSearch.trim()), 300);
    return () => clearTimeout(t);
  }, [tableSearch]);

  type ToolsPageResp = { items: any[]; total: number; page: number; page_size: number };
  const toolsPage = useQuery({
    queryKey: ["tools", "page", { page, pageSize, q: debouncedSearch }],
    queryFn: () =>
      api<ToolsPageResp>(
        `/api/tools?page=${page}&page_size=${pageSize}&q=${encodeURIComponent(debouncedSearch)}`
      ),
    placeholderData: keepPreviousData,
  });
  const pageItems = toolsPage.data?.items ?? [];
  const pageTotal = toolsPage.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(pageTotal / pageSize));
  const pageStart = pageTotal === 0 ? 0 : (page - 1) * pageSize;
  const pageEnd = pageStart + pageItems.length;
  useEffect(() => {
    if (page > totalPages) setPage(totalPages);
  }, [totalPages, page]);
  useEffect(() => {
    setPage(1);
  }, [debouncedSearch, pageSize]);

  // SQL autocomplete schema derived from the currently introspected tables.
  // Maps "schema.table" (or just "table") to its column names so the editor
  // can suggest both table identifiers and their columns.
  const sqlSchema = useMemo<SqlSchema>(() => {
    const out: SqlSchema = {};
    for (const t of tables ?? []) {
      const cols = (t.columns || []).map((c: any) => String(c.name));
      const qualified = t.schema ? `${t.schema}.${t.name}` : String(t.name);
      out[qualified] = cols;
      if (t.schema) out[String(t.name)] = cols;
    }
    return out;
  }, [tables]);

  function toggleHelp() {
    setHelpOpen((v) => {
      const next = !v;
      try {
        localStorage.setItem(HELP_DISMISSED_KEY, next ? "0" : "1");
      } catch {}
      return next;
    });
  }

  // ===== Mention autocomplete (@conn.schema.table.col / #tool_slug) =====
  const [schemaCache, setSchemaCache] = useState<Record<string, any[]>>({});
  const [toolDetailCache, setToolDetailCache] = useState<Record<string, any>>({});
  type MentionItem = { label: string; insert: string; sub?: string };
  const [mentionMenu, setMentionMenu] = useState<{
    trigger: "@" | "#";
    start: number;
    query: string;
    items: MentionItem[];
    activeIdx: number;
    loading?: boolean;
  } | null>(null);
  const [ctxPickerOpen, setCtxPickerOpen] = useState(false);

  const [testTool, setTestTool] = useState<any | null>(null);
  const [testParams, setTestParams] = useState<Record<string, any>>({});
  const [testResult, setTestResult] = useState<any | null>(null);
  const [testError, setTestError] = useState<string | null>(null);
  const [testRunning, setTestRunning] = useState(false);

  const formRef = useRef<HTMLDivElement | null>(null);

  const resetForm = () => {
    setEditingId(null);
    setF(INITIAL_FORM);
    setChat([]);
    setLastTest(null);
    setTables([]);
  };

  const startEdit = async (id: string) => {
    try {
      const t = await api<any>(`/api/tools/${id}`);
      setEditingId(id);
      setF({
        kind: (t.kind ?? t.Kind ?? "query") as "query" | "code",
        group_id: t.group_id ?? t.GroupID ?? "",
        connection_id: t.connection_id ?? t.ConnectionID ?? "",
        slug: t.slug ?? t.Slug ?? "",
        title: t.title ?? t.Title ?? "",
        description: t.description ?? t.Description ?? "",
        query_text: t.query_text ?? t.QueryText ?? "",
        params_schema: JSON.stringify(t.params_schema ?? t.ParamsSchema ?? {}, null, 2),
        row_limit: t.row_limit ?? t.RowLimit ?? 1000,
        timeout_ms: t.timeout_ms ?? t.TimeoutMS ?? 15000,
        cache_ttl_sec: t.cache_ttl_sec ?? t.CacheTTLSec ?? 0,
        cache_per_token: t.cache_per_token ?? t.CachePerToken ?? false,
        test_params: {},
        activate: (t.status ?? t.Status) === "active",
        code_refs: (t.code_refs ?? t.CodeRefs ?? { connection_ids: [], tool_slugs: [] }) as {
          connection_ids: string[];
          tool_slugs: string[];
        },
      });
      // Hydrate persisted assistant conversation and last preview result so
      // the LLM has continuity across edit sessions.
      const loadedChat = (t.chat_log ?? t.ChatLog ?? []) as any[];
      setChat(
        Array.isArray(loadedChat)
          ? loadedChat
              .filter((m) => m && typeof m === "object" && m.content)
              .map((m) => ({
                role: m.role === "user" ? "user" : "assistant",
                content: String(m.content),
                query: m.query,
                params: m.params,
                slug: m.slug,
                title: m.title,
                description: m.description,
                notes: m.notes,
              }) as ChatMsg)
          : [],
      );
      const lt = (t.last_test ?? t.LastTest ?? null) as LastTest | null;
      setLastTest(lt && typeof lt === "object" ? lt : null);
      formRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    } catch (e) {
      alert("Erro ao carregar tool: " + (e as Error).message);
    }
  };

  useEffect(() => {
    chatEnd.current?.scrollIntoView({ behavior: "smooth" });
  }, [chat]);

  // "Promover a Tool" bridge (Fase 3a): when Query Studio navigates here with a
  // PromoteToolState in the router state, seed a fresh query-tool draft with the
  // query/connection/explanation already filled in. We clear the router state
  // afterwards so a refresh or back-navigation does not re-apply the prefill.
  useEffect(() => {
    const promote = (location.state as { promote?: PromoteToolState } | null)?.promote;
    if (!promote) return;
    setEditingId(null);
    setF({
      ...INITIAL_FORM,
      kind: "query",
      connection_id: promote.connection_id ?? "",
      query_text: promote.query_text || INITIAL_FORM.query_text,
      title: promote.title ?? "",
      description: promote.description ?? "",
    });
    setChat([
      {
        role: "assistant",
        content:
          "Query importada do Query Studio. Ajuste slug, título e parâmetros conforme " +
          "necessário e salve (o dry-run roda antes de ativar).",
      },
    ]);
    setLastTest(null);
    setTables([]);
    navigate(location.pathname, { replace: true, state: null });
    formRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state]);

  // Reset introspect cache when connection changes. We do NOT clear the
  // chat log here anymore: chat history belongs to the tool, not to a
  // particular connection (and persists across edit sessions).
  useEffect(() => {
    setTables([]);
  }, [f.connection_id]);

  // Warm caches for pinned context whenever it changes (introspect connections,
  // fetch tool details) so the LLM always has full schema available.
  useEffect(() => {
    if (!conns.data) return;
    for (const cid of f.code_refs.connection_ids) {
      const conn = conns.data.find((c: any) => c.id === cid);
      if (conn && !schemaCache[conn.name]) {
        loadConnSchema(conn.name);
      }
    }
    if (!tools.data) return;
    for (const slug of f.code_refs.tool_slugs) {
      if (toolDetailCache[slug]) continue;
      const t = tools.data.find((x: any) => x.slug === slug);
      if (!t) continue;
      api<any>(`/api/tools/${t.id}`)
        .then((d) => setToolDetailCache((p) => ({ ...p, [slug]: d })))
        .catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    conns.data,
    tools.data,
    f.code_refs.connection_ids.join(","),
    f.code_refs.tool_slugs.join(","),
  ]);

  const introspect = useMutation({
    mutationFn: () =>
      api<any[]>(`/api/connections/${f.connection_id}/introspect`),
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

  const send = useMutation({
    mutationFn: async (userText: string): Promise<ChatResp> => {
      const history = [...chat, { role: "user", content: userText } as ChatMsg].map((m) => ({
        role: m.role,
        content: m.content,
      }));
      let paramsSchema: any = undefined;
      if (f.params_schema && f.params_schema.trim()) {
        try {
          paramsSchema = JSON.parse(f.params_schema);
        } catch {
          paramsSchema = undefined;
        }
      }
      const current = {
        editing: !!editingId,
        slug: f.slug || undefined,
        title: f.title || undefined,
        description: f.description || undefined,
        query_text: f.query_text || undefined,
        params_schema: paramsSchema,
      };
      return api<ChatResp>("/api/llm/chat-tool", {
        method: "POST",
        body: JSON.stringify({
          connection_id: f.connection_id || undefined,
          tables,
          messages: history,
          current,
        }),
      });
    },
    onSuccess: (res) => {
      setChat((c) => [
        ...c,
        {
          role: "assistant",
          content: res.reply,
          query: res.query,
          params: res.params,
          slug: res.slug,
          title: res.title,
          description: res.description,
          notes: res.notes,
        },
      ]);
    },
  });

  const submitChat = async (e?: React.FormEvent) => {
    e?.preventDefault();
    const text = chatInput.trim();
    if (!text) return;
    const ctx = await buildMentionContext(text);
    const enhanced = ctx ? `${text}\n\n---\nReferences (use these as the source of truth):\n${ctx}` : text;
    if (f.kind === "code") {
      if (generateCode.isPending) return;
      setChat((c) => [...c, { role: "user", content: text }]);
      setChatInput("");
      setMentionMenu(null);
      generateCode.mutate(enhanced);
      return;
    }
    if (send.isPending) return;
    setChat((c) => [...c, { role: "user", content: text }]);
    setChatInput("");
    setMentionMenu(null);
    send.mutate(enhanced);
  };

  const useProposal = (msg: ChatMsg) => {
    if (!msg.query) return;
    setF((p) => ({
      ...p,
      query_text: msg.query!,
      params_schema: paramsToSchema(msg.params || []),
      slug: msg.slug || p.slug,
      title: msg.title || p.title,
      description: msg.description || p.description,
    }));
  };

  const generateCode = useMutation({
    mutationFn: async (enhancedPrompt: string) => {
      if (!enhancedPrompt) throw new Error("Digite um prompt no chat antes");
      const history = [...chat, { role: "user", content: enhancedPrompt } as ChatMsg].map((m) => ({
        role: m.role,
        content: m.content,
      }));
      let paramsSchema: any = undefined;
      if (f.params_schema && f.params_schema.trim()) {
        try {
          paramsSchema = JSON.parse(f.params_schema);
        } catch {
          paramsSchema = undefined;
        }
      }
      const current = {
        editing: !!editingId,
        slug: f.slug || undefined,
        title: f.title || undefined,
        description: f.description || undefined,
        query_text: f.query_text || undefined,
        params_schema: paramsSchema,
      };
      return api<{
        reply?: string;
        code: string;
        params?: ParamSpec[];
        slug?: string;
        title?: string;
        description?: string;
        notes?: string;
      }>("/api/llm/generate-code", {
        method: "POST",
        body: JSON.stringify({ prompt: enhancedPrompt, messages: history, current }),
      });
    },
    onSuccess: (res) => {
      if (res.code) {
        setF((p) => ({
          ...p,
          kind: "code",
          query_text: res.code,
          params_schema: paramsToSchema(res.params || []),
        }));
      }
      setChat((c) => [
        ...c,
        {
          role: "assistant",
          content: res.reply || res.notes || "Snippet gerado e aplicado no formulário.",
          query: res.code || undefined,
          params: res.params,
          slug: res.slug,
          title: res.title,
          description: res.description,
          notes: res.notes,
        },
      ]);
    },
  });

  // ===== Mention helpers =====
  const loadConnSchema = async (connName: string): Promise<any[]> => {
    if (schemaCache[connName]) return schemaCache[connName];
    const conn = conns.data?.find((c: any) => c.name === connName);
    if (!conn) return [];
    try {
      const data = await api<any[]>(`/api/connections/${conn.id}/introspect`);
      const list = data || [];
      setSchemaCache((p) => ({ ...p, [connName]: list }));
      return list;
    } catch {
      setSchemaCache((p) => ({ ...p, [connName]: [] }));
      return [];
    }
  };

  const computeMentionItems = async (
    trigger: "@" | "#",
    text: string,
  ): Promise<MentionItem[]> => {
    if (trigger === "#") {
      const q = text.toLowerCase();
      return (tools.data || [])
        .filter(
          (t: any) =>
            !q ||
            t.slug.toLowerCase().includes(q) ||
            (t.title || "").toLowerCase().includes(q),
        )
        .slice(0, 12)
        .map((t: any) => ({ label: t.slug, insert: t.slug, sub: t.title || "" }));
    }
    const parts = text.split(".");
    if (parts.length === 1) {
      const q = parts[0].toLowerCase();
      return (conns.data || [])
        .filter((c: any) => !q || c.name.toLowerCase().includes(q))
        .slice(0, 12)
        .map((c: any) => ({ label: c.name, insert: c.name, sub: c.type }));
    }
    const connName = parts[0];
    const tabs = await loadConnSchema(connName);
    const schemas = Array.from(
      new Set(tabs.map((t: any) => t.schema || "").filter(Boolean)),
    ) as string[];
    if (parts.length === 2) {
      const q = parts[1].toLowerCase();
      const schemaMatches: MentionItem[] = schemas
        .filter((s) => s.toLowerCase().includes(q))
        .map((s) => ({
          label: `${connName}.${s}`,
          insert: `${connName}.${s}`,
          sub: "schema",
        }));
      const tableNoSchema: MentionItem[] = tabs
        .filter((t: any) => !t.schema && t.name.toLowerCase().includes(q))
        .map((t: any) => ({
          label: `${connName}.${t.name}`,
          insert: `${connName}.${t.name}`,
          sub: "table",
        }));
      return [...schemaMatches, ...tableNoSchema].slice(0, 12);
    }
    if (parts.length === 3) {
      const schema = parts[1];
      const q = parts[2].toLowerCase();
      return tabs
        .filter(
          (t: any) => (t.schema || "") === schema && t.name.toLowerCase().includes(q),
        )
        .slice(0, 12)
        .map((t: any) => ({
          label: `${connName}.${schema}.${t.name}`,
          insert: `${connName}.${schema}.${t.name}`,
          sub: "table",
        }));
    }
    if (parts.length === 4) {
      const schema = parts[1];
      const tname = parts[2];
      const q = parts[3].toLowerCase();
      const t = tabs.find(
        (x: any) => (x.schema || "") === schema && x.name === tname,
      );
      return (t?.columns || [])
        .filter((c: any) => c.name.toLowerCase().includes(q))
        .slice(0, 12)
        .map((c: any) => ({
          label: `${connName}.${schema}.${tname}.${c.name}`,
          insert: `${connName}.${schema}.${tname}.${c.name}`,
          sub: c.type,
        }));
    }
    return [];
  };

  const detectMention = (text: string, cursor: number) => {
    const before = text.slice(0, cursor);
    const m = before.match(/(^|[^\w.-])([@#])([\w.-]*)$/);
    if (!m) return null;
    const trigger = m[2] as "@" | "#";
    const query = m[3];
    const start = before.length - query.length - 1;
    return { trigger, query, start };
  };

  const onChatInputChange = (newText: string, cursorPos: number) => {
    setChatInput(newText);
    const hit = detectMention(newText, cursorPos);
    if (!hit) {
      setMentionMenu(null);
      return;
    }
    setMentionMenu({
      trigger: hit.trigger,
      start: hit.start,
      query: hit.query,
      items: [],
      activeIdx: 0,
      loading: true,
    });
    computeMentionItems(hit.trigger, hit.query).then((items) => {
      setMentionMenu((prev) => {
        if (!prev || prev.start !== hit.start || prev.trigger !== hit.trigger) {
          return prev;
        }
        return { ...prev, items, activeIdx: 0, loading: false };
      });
    });
  };

  const applyMention = (item: MentionItem) => {
    if (!mentionMenu) return;
    const before = chatInput.slice(0, mentionMenu.start);
    const after = chatInput.slice(
      mentionMenu.start + 1 + mentionMenu.query.length,
    );
    const insertText = mentionMenu.trigger + item.insert;
    const newText = before + insertText + " " + after;
    setChatInput(newText);
    // Auto-pin the referenced connection or tool to the tool's context.
    if (mentionMenu.trigger === "@") {
      const connName = item.insert.split(".")[0];
      const conn = conns.data?.find((c: any) => c.name === connName);
      if (conn) addPinnedConn(conn.id);
    } else if (mentionMenu.trigger === "#") {
      addPinnedTool(item.insert);
    }
    setMentionMenu(null);
    requestAnimationFrame(() => {
      if (chatInputRef.current) {
        const pos = before.length + insertText.length + 1;
        chatInputRef.current.selectionStart = pos;
        chatInputRef.current.selectionEnd = pos;
        chatInputRef.current.focus();
      }
    });
  };

  // ===== Pinned context helpers =====
  const addPinnedConn = (connId: string) => {
    setF((p) => {
      if (p.code_refs.connection_ids.includes(connId)) return p;
      return {
        ...p,
        code_refs: {
          ...p.code_refs,
          connection_ids: [...p.code_refs.connection_ids, connId],
        },
      };
    });
  };
  const removePinnedConn = (connId: string) => {
    setF((p) => ({
      ...p,
      code_refs: {
        ...p.code_refs,
        connection_ids: p.code_refs.connection_ids.filter((x) => x !== connId),
      },
    }));
  };
  const addPinnedTool = (slug: string) => {
    setF((p) => {
      if (p.code_refs.tool_slugs.includes(slug)) return p;
      return {
        ...p,
        code_refs: { ...p.code_refs, tool_slugs: [...p.code_refs.tool_slugs, slug] },
      };
    });
  };
  const removePinnedTool = (slug: string) => {
    setF((p) => ({
      ...p,
      code_refs: {
        ...p.code_refs,
        tool_slugs: p.code_refs.tool_slugs.filter((x) => x !== slug),
      },
    }));
  };

  const buildMentionContext = async (text: string): Promise<string> => {
    const lines: string[] = [];
    // 1) Pinned connections: emit full schema (every table + its columns).
    //    This is what gives the LLM persistent knowledge of the data model
    //    across edits, even when the current user message has no @mentions.
    for (const cid of f.code_refs.connection_ids) {
      const conn = conns.data?.find((c: any) => c.id === cid);
      if (!conn) continue;
      const tabs = await loadConnSchema(conn.name);
      lines.push(
        `Connection @${conn.name} (${conn.type}) - ${tabs.length} table(s):`,
      );
      for (const t of tabs.slice(0, 80)) {
        const cols = (t.columns || [])
          .map((c: any) => c.name + ":" + c.type + (c.nullable ? "?" : ""))
          .join(", ");
        const qualified = t.schema ? `${t.schema}.${t.name}` : t.name;
        lines.push(`  - ${qualified} [${cols}]`);
      }
    }
    // 2) Pinned tools: full title/description/params + output shape.
    for (const slug of f.code_refs.tool_slugs) {
      const t = (tools.data || []).find((x: any) => x.slug === slug);
      if (!t) continue;
      let detail = toolDetailCache[slug];
      if (!detail) {
        try {
          detail = await api<any>(`/api/tools/${t.id}`);
          setToolDetailCache((p) => ({ ...p, [slug]: detail }));
        } catch {
          continue;
        }
      }
      lines.push(`Tool #${slug} - ${detail.title || ""}`);
      if (detail.description) lines.push(`  description: ${detail.description}`);
      const schema = detail.params_schema || {};
      const props = (schema.properties || {}) as Record<string, any>;
      const required = (schema.required || []) as string[];
      for (const [n, p] of Object.entries(props)) {
        lines.push(
          `  param ${n}: ${p.type}${required.includes(n) ? " (required)" : ""}${p.description ? " - " + p.description : ""}`,
        );
      }
      // Output shape: prefer declared output_schema; else infer SELECT
      // aliases from the SQL so the LLM stops hallucinating column names.
      const outSchema = detail.output_schema;
      const outProps =
        outSchema && typeof outSchema === "object" ? outSchema.properties : null;
      if (outProps && typeof outProps === "object" && Object.keys(outProps).length) {
        const cols = Object.entries(outProps as Record<string, any>)
          .map(([n, p]) => `${n}:${(p && p.type) || "any"}`)
          .join(", ");
        lines.push(`  returns columns: [${cols}]`);
      } else if (
        (detail.kind === "query" || detail.Kind === "query") &&
        typeof detail.query_text === "string"
      ) {
        const cols = extractSelectAliases(detail.query_text);
        if (cols.length) {
          lines.push(`  returns columns (inferred from SELECT): [${cols.join(", ")}]`);
        }
      }
    }
    // 3) Column-level emphasis for any @conn.schema.table.col mention in the
    //    current message (depths 1-3 are already covered by pinned schema).
    const atRe = /(?:^|\s)@([\w.-]+)/g;
    const seen = new Set<string>();
    let mm: RegExpExecArray | null;
    while ((mm = atRe.exec(text)) !== null) {
      const ref = mm[1].replace(/\.+$/, "");
      if (!ref || seen.has("@" + ref)) continue;
      seen.add("@" + ref);
      const parts = ref.split(".");
      if (parts.length < 4) continue;
      const tabs = await loadConnSchema(parts[0]);
      const t = tabs.find(
        (x: any) => (x.schema || "") === parts[1] && x.name === parts[2],
      );
      if (!t) continue;
      const c = (t.columns || []).find((c: any) => c.name === parts[3]);
      if (c) {
        lines.push(
          `User focus @${ref}: column ${c.name} ${c.type}${c.nullable ? " (nullable)" : ""}`,
        );
      }
    }
    // 4) Last test result (success or failure) — gives the LLM the actual
    //    runtime behavior of the current draft so iterative fixes converge.
    if (lastTest) {
      lines.push("");
      lines.push(`Last test (${lastTest.ts}, status=${lastTest.status}):`);
      if (lastTest.params !== undefined) {
        lines.push(`  params: ${JSON.stringify(lastTest.params)}`);
      }
      if (lastTest.status === "error") {
        lines.push(`  error: ${lastTest.error ?? "unknown"}`);
      } else {
        if (lastTest.columns?.length) {
          lines.push(`  columns: [${lastTest.columns.join(", ")}]`);
        }
        if (typeof lastTest.row_count === "number") {
          lines.push(`  row_count: ${lastTest.row_count}`);
        }
        if (lastTest.sample_rows?.length) {
          lines.push(`  sample_rows:`);
          for (const r of lastTest.sample_rows) {
            lines.push(`    ${JSON.stringify(r)}`);
          }
        }
      }
    }
    return lines.join("\n");
  };

  const fixError = useMutation({
    mutationFn: async (errorMsg: string): Promise<{
      reply?: string;
      query?: string;
      params?: ParamSpec[];
      slug?: string;
      title?: string;
      description?: string;
      notes?: string;
    }> => {
      const cleanError = errorMsg.replace(/^dry-run failed:\s*/i, "");
      const prompt =
        `O dry-run da tool falhou com o seguinte erro:\n\n${cleanError}\n\n` +
        `Contexto importante sobre o dry-run:\n` +
        `- O dry-run é executado automaticamente antes de salvar a tool e usa valores PADRÃO para todos os parâmetros declarados no params_schema:\n` +
        `  number/integer => 0, string => "", boolean => false, array => [], object => {}.\n` +
        `- Por isso, validações como "if (!params.x) throw ..." rejeitam o próprio valor padrão e quebram o dry-run.\n` +
        `- Use "params.x === undefined" ou "params.x == null" para checar parâmetros obrigatórios.\n` +
        `- Para enums de string, aceite a string vazia retornando um resultado vazio em vez de lançar erro.\n\n` +
        `Analise o código/query atual (já enviado como rascunho) e devolva uma versão corrigida que passe no dry-run com esses valores padrão.`;
      const history = [
        ...chat,
        { role: "user" as const, content: prompt },
      ].map((m) => ({ role: m.role, content: m.content }));
      let paramsSchema: any = undefined;
      if (f.params_schema && f.params_schema.trim()) {
        try {
          paramsSchema = JSON.parse(f.params_schema);
        } catch {
          paramsSchema = undefined;
        }
      }
      const current = {
        editing: !!editingId,
        slug: f.slug || undefined,
        title: f.title || undefined,
        description: f.description || undefined,
        query_text: f.query_text || undefined,
        params_schema: paramsSchema,
      };
      if (f.kind === "code") {
        const res = await api<{
          reply?: string;
          code: string;
          params?: ParamSpec[];
          slug?: string;
          title?: string;
          description?: string;
          notes?: string;
        }>("/api/llm/generate-code", {
          method: "POST",
          body: JSON.stringify({ prompt, messages: history, current }),
        });
        return { reply: res.reply, query: res.code, params: res.params, slug: res.slug, title: res.title, description: res.description, notes: res.notes };
      } else {
        const res = await api<ChatResp>("/api/llm/chat-tool", {
          method: "POST",
          body: JSON.stringify({ connection_id: f.connection_id || undefined, tables, messages: history, current }),
        });
        return { reply: res.reply, query: res.query, params: res.params, slug: res.slug, title: res.title, description: res.description, notes: res.notes };
      }
    },
    onSuccess: (res, errorMsg) => {
      const cleanError = errorMsg.replace(/^dry-run failed:\s*/i, "");
      const userVisible = `Pedi ao assistente para corrigir o erro do dry-run:\n\n${cleanError}`;
      setChat((c) => [
        ...c,
        { role: "user", content: userVisible },
        {
          role: "assistant",
          content: res.reply || res.notes || "Correção sugerida. Revise e aceite se estiver correto.",
          query: res.query || undefined,
          params: res.params,
          slug: res.slug,
          title: res.title,
          description: res.description,
          notes: res.notes,
          isProposal: true,
        },
      ]);
    },
  });

  const save = useMutation({
    mutationFn: (formData: typeof f) => {
      let parsedSchema: any = { type: "object", properties: {} };
      try {
        parsedSchema = JSON.parse(formData.params_schema);
      } catch {
        throw new Error("params_schema não é um JSON válido");
      }
      const { group_id, connection_id, ...rest } = formData;
      const body: any = {
        ...rest,
        params_schema: parsedSchema,
        chat_log: chat,
        last_test: lastTest,
      };
      if (group_id) body.group_id = group_id;
      if (connection_id) body.connection_id = connection_id;
      if (editingId) {
        return api(`/api/tools/${editingId}`, { method: "PUT", body: JSON.stringify(body) });
      }
      return api("/api/tools", { method: "POST", body: JSON.stringify(body) });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tools"], refetchType: "active" });
      resetForm();
    },
    onError: (err: any) => {
      // Capture dry-run failures so the next assistant turn can see what
      // actually broke (the LLM does not get the HTTP error otherwise).
      const msg = String(err?.message ?? err ?? "");
      if (/dry-run failed/i.test(msg)) {
        const cleaned = msg.replace(/^dry-run failed:\s*/i, "");
        const lt: LastTest = { ts: new Date().toISOString(), status: "error", error: cleaned };
        setLastTest(lt);
        setChat((c) => [
          ...c,
          {
            role: "assistant",
            content: `Observação do sistema: dry-run falhou ao salvar — ${cleaned}`,
          },
        ]);
      }
    },
  });

  const acceptProposal = (msg: ChatMsg) => {
    const newF: typeof f = {
      ...f,
      query_text: msg.query || f.query_text,
      params_schema: msg.params ? paramsToSchema(msg.params) : f.params_schema,
      slug: msg.slug || f.slug,
      title: msg.title || f.title,
      description: msg.description || f.description,
    };
    setF(newF);
    save.mutate(newF);
  };

  const remove = useMutation({
    mutationFn: (id: string) => api(`/api/tools/${id}`, { method: "DELETE" }),
    onSuccess: (_d, id) => {
      qc.invalidateQueries({ queryKey: ["tools"], refetchType: "active" });
      if (editingId === id) resetForm();
    },
  });

  const invalidate = useMutation({
    mutationFn: (id: string) => api(`/api/tools/${id}/cache/invalidate`, { method: "POST" }),
  });

  const openTest = async (toolRow: any) => {
    try {
      const t = await api<any>(`/api/tools/${toolRow.id}`);
      const schema = t.params_schema ?? t.ParamsSchema ?? {};
      const props: Record<string, any> = schema.properties || {};
      const initial: Record<string, any> = {};
      for (const name of Object.keys(props)) {
        const ty = (props[name]?.type || "string").toLowerCase();
        if (ty === "number" || ty === "integer") initial[name] = "";
        else if (ty === "boolean") initial[name] = false;
        else if (ty === "array") initial[name] = "";
        else if (ty === "object") initial[name] = "";
        else initial[name] = "";
      }
      setTestTool({ ...t, _schema: schema, _props: props });
      setTestParams(initial);
      setTestResult(null);
      setTestError(null);
    } catch (e) {
      alert("Erro ao carregar tool: " + (e as Error).message);
    }
  };

  const runTest = async () => {
    if (!testTool) return;
    setTestRunning(true);
    setTestError(null);
    setTestResult(null);
    try {
      const props: Record<string, any> = testTool._props || {};
      const coerced: Record<string, any> = {};
      for (const [name, raw] of Object.entries(testParams)) {
        const ty = (props[name]?.type || "string").toLowerCase();
        if (raw === "" || raw === undefined || raw === null) continue;
        if (ty === "number") {
          const n = Number(raw);
          if (Number.isNaN(n)) throw new Error(`Param "${name}" não é um número válido`);
          coerced[name] = n;
        } else if (ty === "integer") {
          const n = parseInt(String(raw), 10);
          if (Number.isNaN(n)) throw new Error(`Param "${name}" não é um inteiro válido`);
          coerced[name] = n;
        } else if (ty === "boolean") {
          coerced[name] = !!raw;
        } else if (ty === "array" || ty === "object") {
          try {
            coerced[name] = JSON.parse(String(raw));
          } catch {
            throw new Error(`Param "${name}" deve ser JSON válido`);
          }
        } else {
          coerced[name] = String(raw);
        }
      }
      const res = await api<any>(`/api/tools/${testTool.id}/run`, {
        method: "POST",
        body: JSON.stringify({ params: coerced }),
      });
      setTestResult(res);
      // Snapshot for assistant context.
      const data = res?.data ?? res;
      const cols: string[] = Array.isArray(data?.columns) ? data.columns : [];
      const rows: any[] = Array.isArray(data?.rows) ? data.rows : [];
      const lt: LastTest = {
        ts: new Date().toISOString(),
        status: "ok",
        params: coerced,
        columns: cols,
        sample_rows: rows.slice(0, 3),
        row_count: typeof data?.count === "number" ? data.count : rows.length,
      };
      setLastTest(lt);
      // If this test is for the tool currently being edited, append an
      // observation to the chat so the next assistant turn can react.
      if (editingId && testTool.id === editingId) {
        setChat((c) => [
          ...c,
          {
            role: "assistant",
            content:
              `Observação do sistema: teste executado em ${new Date(lt.ts).toLocaleString()} ` +
              `com params ${JSON.stringify(coerced)} → ${lt.row_count ?? 0} linha(s); ` +
              `colunas: [${cols.join(", ")}]` +
              (rows.length
                ? `\nPrimeira linha: ${JSON.stringify(rows[0])}`
                : ""),
          },
        ]);
      }
    } catch (e) {
      const msg = (e as Error).message;
      setTestError(msg);
      const lt: LastTest = {
        ts: new Date().toISOString(),
        status: "error",
        params: testParams,
        error: msg,
      };
      setLastTest(lt);
      if (editingId && testTool && testTool.id === editingId) {
        setChat((c) => [
          ...c,
          {
            role: "assistant",
            content: `Observação do sistema: teste falhou — ${msg}`,
          },
        ]);
      }
    } finally {
      setTestRunning(false);
    }
  };

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">Tools</h2>

      <div ref={formRef} className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {/* ============== Form ============== */}
        <div className="border border-border rounded bg-white p-4 space-y-3">
          <div className="flex items-center justify-between">
            <div className="font-semibold">
              {editingId ? `Editando tool — ${f.slug}` : "Nova tool"}
            </div>
            {editingId && (
              <button
                type="button"
                className="text-xs px-2 py-1 border border-border rounded hover:bg-muted"
                onClick={resetForm}
              >
                Cancelar edição
              </button>
            )}
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <label className="sm:col-span-2 text-xs font-medium text-muted-foreground">
              Tipo
            </label>
            <div className="sm:col-span-2 flex gap-4 text-sm">
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name="tool-kind"
                  value="query"
                  checked={f.kind === "query"}
                  onChange={() => setF({ ...f, kind: "query" })}
                />
                Query (SQL / Mongo / REST em uma conexão)
              </label>
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name="tool-kind"
                  value="code"
                  checked={f.kind === "code"}
                  onChange={() =>
                    setF((p) => ({
                      ...p,
                      kind: "code",
                      query_text:
                        p.query_text && p.query_text !== "SELECT 1 AS n"
                          ? p.query_text
                          : DEFAULT_CODE_BODY,
                    }))
                  }
                />
                Código (JavaScript)
              </label>
            </div>
            {f.kind === "query" && (
              <select
                className="border border-border rounded px-3 py-2"
                value={f.connection_id}
                onChange={(e) => setF({ ...f, connection_id: e.target.value })}
              >
                <option value="">— connection —</option>
                {conns.data?.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} ({c.type})
                  </option>
                ))}
              </select>
            )}
            <select
              className="border border-border rounded px-3 py-2"
              value={f.group_id}
              onChange={(e) => setF({ ...f, group_id: e.target.value })}
            >
              <option value="">— group —</option>
              {groups.data?.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
            </select>
            <input
              className="border border-border rounded px-3 py-2"
              placeholder="slug (a-z0-9_)"
              value={f.slug}
              onChange={(e) => setF({ ...f, slug: e.target.value })}
            />
            <input
              className="border border-border rounded px-3 py-2"
              placeholder="title"
              value={f.title}
              onChange={(e) => setF({ ...f, title: e.target.value })}
            />
            <input
              className="sm:col-span-2 border border-border rounded px-3 py-2"
              placeholder="description"
              value={f.description}
              onChange={(e) => setF({ ...f, description: e.target.value })}
            />
            <p className="sm:col-span-2 -mt-1 text-xs text-muted-foreground">
              Dica: no chat do assistente abaixo, use <code className="font-mono">@</code> para
              referenciar <strong>tabelas/colunas</strong> e <code className="font-mono">#</code>{" "}
              para referenciar <strong>outras tools</strong>.
            </p>

            <label className="sm:col-span-2 text-xs font-medium text-muted-foreground">
              {f.kind === "code" ? "Código JavaScript" : "Query"}
            </label>
            <div className="sm:col-span-2">
              <CodeEditor
                value={f.query_text}
                onChange={(v) => setF({ ...f, query_text: v })}
                language={f.kind === "code" ? "javascript" : "sql"}
                height="10rem"
                label={f.kind === "code" ? "JavaScript" : "SQL"}
                sqlSchema={sqlSchema}
              />
            </div>

            <label className="sm:col-span-2 text-xs font-medium text-muted-foreground">
              params_schema (JSON Schema)
            </label>
            <div className="sm:col-span-2">
              <CodeEditor
                value={f.params_schema}
                onChange={(v) => setF({ ...f, params_schema: v })}
                language="json"
                height="7rem"
                label="JSON Schema"
              />
            </div>

            <input
              type="number"
              className="border border-border rounded px-3 py-2"
              placeholder="row_limit"
              value={f.row_limit}
              onChange={(e) => setF({ ...f, row_limit: +e.target.value })}
            />
            <input
              type="number"
              className="border border-border rounded px-3 py-2"
              placeholder="timeout_ms"
              value={f.timeout_ms}
              onChange={(e) => setF({ ...f, timeout_ms: +e.target.value })}
            />
            <input
              type="number"
              className="border border-border rounded px-3 py-2"
              placeholder="cache_ttl_sec"
              value={f.cache_ttl_sec}
              onChange={(e) => setF({ ...f, cache_ttl_sec: +e.target.value })}
            />
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={f.cache_per_token}
                onChange={(e) => setF({ ...f, cache_per_token: e.target.checked })}
              />
              Cache por token
            </label>

            <button
              className="sm:col-span-2 bg-primary text-white rounded py-2"
              onClick={() => save.mutate(f)}
              disabled={save.isPending}
            >
              {save.isPending
                ? "..."
                : editingId
                ? "Salvar alterações (testa antes de ativar)"
                : "Criar (testa antes de ativar)"}
            </button>
            {save.error && (
              <div className="sm:col-span-2 space-y-2">
                <div className="text-sm text-red-600">
                  {(save.error as Error).message}
                </div>
                {(save.error as Error).message.toLowerCase().includes("dry-run failed") && (
                  <button
                    type="button"
                    className="text-xs px-3 py-1.5 bg-amber-500 text-white rounded hover:opacity-90 disabled:opacity-50"
                    onClick={() => fixError.mutate((save.error as Error).message)}
                    disabled={fixError.isPending}
                  >
                    {fixError.isPending ? "Consultando assistente..." : "Corrigir com assistente"}
                  </button>
                )}
              </div>
            )}
          </div>
        </div>

        {/* ============== Chat ============== */}
        <div className="border border-border rounded bg-white flex flex-col h-[640px]">
          <div className="flex items-center justify-between border-b border-border px-4 py-2">
            <div className="font-semibold">Assistente</div>
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground">
                {f.kind === "code"
                  ? "modo código (JS) — sem schema de conexão"
                  : tables.length > 0
                  ? `${tables.length} tabela(s) no contexto`
                  : "schema não carregado"}
              </span>
              {f.kind === "query" && (
                <button
                  type="button"
                  className="text-xs px-2 py-1 border border-border rounded hover:bg-muted disabled:opacity-50"
                  onClick={() => introspect.mutate()}
                  disabled={!f.connection_id || introspect.isPending}
                  title="Carrega tabelas e colunas da conexão para o LLM"
                >
                  {introspect.isPending ? "..." : "Carregar schema"}
                </button>
              )}
              <button
                type="button"
                className="text-xs px-2 py-1 border border-border rounded hover:bg-muted"
                onClick={toggleHelp}
                title="Mostrar/ocultar dicas de uso"
              >
                {helpOpen ? "Ocultar ajuda" : "Ajuda"}
              </button>
              <button
                type="button"
                className="text-xs px-2 py-1 border border-border rounded hover:bg-muted"
                onClick={() => setChat([])}
                disabled={chat.length === 0}
              >
                Limpar
              </button>
            </div>
          </div>

          {helpOpen && (
            <div className="border-b border-border bg-blue-50/60 px-4 py-3 text-xs text-slate-700 space-y-2">
              <div className="font-semibold text-slate-800">Como usar o assistente</div>
              <ul className="list-disc pl-5 space-y-1">
                <li>
                  Digite <code className="font-mono bg-white border border-border rounded px-1">@</code>{" "}
                  para referenciar <strong>tabelas e colunas</strong> do banco. Exemplo:{" "}
                  <code className="font-mono bg-white border border-border rounded px-1">
                    @minha_conn.public.clientes.email
                  </code>
                  . Um menu de autocomplete aparece enquanto você digita.
                </li>
                <li>
                  Digite <code className="font-mono bg-white border border-border rounded px-1">#</code>{" "}
                  para referenciar <strong>outras tools</strong> já cadastradas pelo slug. Exemplo:{" "}
                  <code className="font-mono bg-white border border-border rounded px-1">
                    #lookup_cliente
                  </code>
                  . Útil para compor tools (chamando outras dentro de uma tool de código).
                </li>
                <li>
                  Para modo <strong>query</strong>: selecione a connection e clique em{" "}
                  <em>Carregar schema</em> antes de mencionar tabelas com{" "}
                  <code className="font-mono">@</code>.
                </li>
                <li>
                  Pressione <kbd className="font-mono border border-border rounded px-1 bg-white">Enter</kbd>{" "}
                  para enviar,{" "}
                  <kbd className="font-mono border border-border rounded px-1 bg-white">Shift+Enter</kbd>{" "}
                  para nova linha.
                </li>
              </ul>
            </div>
          )}

          <div className="flex-1 overflow-y-auto p-4 space-y-3">
            {chat.length === 0 && (
              <div className="text-sm text-muted-foreground">
                {f.kind === "code" ? (
                  <>
                    Descreva em linguagem natural o que a tool deve fazer (ex.:{" "}
                    <em>
                      "consulte vendas do mês na conn X e enriqueça com a tool
                      lookup_cliente"
                    </em>
                    ). O assistente gera um snippet JS usando{" "}
                    <code>db()</code>, <code>tools.call()</code> e{" "}
                    <code>fetch()</code>.
                  </>
                ) : (
                  <>
                    Selecione uma connection, clique em <em>Carregar schema</em>{" "}
                    e descreva o que você quer extrair. O assistente fará
                    perguntas e proporá uma query — quando gostar, clique em{" "}
                    <em>Usar esta query</em> para popular o formulário ao lado.
                  </>
                )}
              </div>
            )}
            {chat.map((m, i) => (
              <div
                key={i}
                className={
                  m.role === "user"
                    ? "ml-8 bg-primary/10 rounded p-3 text-sm whitespace-pre-wrap"
                    : "mr-8 bg-muted rounded p-3 text-sm whitespace-pre-wrap"
                }
              >
                <div className="text-xs font-semibold text-muted-foreground mb-1">
                  {m.role === "user" ? "Você" : "Assistente"}
                </div>
                <div>{m.content}</div>
                {m.query && (
                  <div className="mt-2 space-y-2">
                    <pre className="font-mono text-xs bg-white border border-border rounded p-2 overflow-x-auto whitespace-pre-wrap">
{m.query}
                    </pre>
                    {m.params && m.params.length > 0 && (
                      <div className="text-xs">
                        <div className="font-semibold mb-1">Parâmetros:</div>
                        <ul className="list-disc pl-5 space-y-0.5">
                          {m.params.map((p) => (
                            <li key={p.name}>
                              <code>{p.name}</code> (
                              {p.type === "array"
                                ? `array<${p.items_type || "string"}>`
                                : p.type}
                              {p.required ? ", obrigatório" : ""}) — {p.description}
                            </li>
                          ))}
                        </ul>
                      </div>
                    )}
                    {m.isProposal ? (
                      <div className="flex gap-2">
                        <button
                          type="button"
                          className="text-xs px-2 py-1 bg-green-600 text-white rounded hover:opacity-90 disabled:opacity-50"
                          onClick={() => acceptProposal(m)}
                          disabled={save.isPending}
                        >
                          {save.isPending ? "Salvando..." : "Aceitar e salvar"}
                        </button>
                        <button
                          type="button"
                          className="text-xs px-2 py-1 border border-border rounded hover:bg-muted"
                          onClick={() => setChat((c) => c.map((msg, j) => j === i ? { ...msg, isProposal: false } : msg))}
                        >
                          Rejeitar
                        </button>
                      </div>
                    ) : (
                      <button
                        type="button"
                        className="text-xs px-2 py-1 bg-primary text-white rounded hover:opacity-90"
                        onClick={() => useProposal(m)}
                      >
                        Usar esta query
                      </button>
                    )}
                  </div>
                )}
              </div>
            ))}
            {send.isPending && (
              <div className="mr-8 bg-muted rounded p-3 text-sm text-muted-foreground">
                Pensando...
              </div>
            )}
            {generateCode.isPending && (
              <div className="mr-8 bg-muted rounded p-3 text-sm text-muted-foreground">
                Gerando código...
              </div>
            )}
            {send.error && (
              <div className="mr-8 bg-red-50 border border-red-200 text-red-700 rounded p-3 text-sm">
                Erro: {(send.error as Error).message}
              </div>
            )}
            {generateCode.error && (
              <div className="mr-8 bg-red-50 border border-red-200 text-red-700 rounded p-3 text-sm">
                Erro ao gerar código: {(generateCode.error as Error).message}
              </div>
            )}
            {fixError.isPending && (
              <div className="mr-8 bg-muted rounded p-3 text-sm text-muted-foreground">
                Consultando assistente para correção...
              </div>
            )}
            {fixError.error && (
              <div className="mr-8 bg-red-50 border border-red-200 text-red-700 rounded p-3 text-sm">
                Erro ao corrigir: {(fixError.error as Error).message}
              </div>
            )}
            {introspect.error && (
              <div className="mr-8 bg-red-50 border border-red-200 text-red-700 rounded p-3 text-sm">
                Erro ao carregar schema: {(introspect.error as Error).message}
              </div>
            )}
            <div ref={chatEnd} />
          </div>

          {/* ============== Pinned context bar ============== */}
          <div className="border-t border-border px-3 py-2 flex items-center gap-1.5 flex-wrap text-xs bg-muted/30">
            <span className="text-muted-foreground font-semibold mr-1">
              Contexto:
            </span>
            {f.code_refs.connection_ids.map((cid) => {
              const c = conns.data?.find((x: any) => x.id === cid);
              const label = c ? c.name : cid.slice(0, 8);
              return (
                <span
                  key={cid}
                  className="inline-flex items-center gap-1 bg-blue-50 border border-blue-200 rounded px-2 py-0.5"
                  title={c ? `${c.name} (${c.type})` : "conexão removida"}
                >
                  <span className="font-mono">@{label}</span>
                  <button
                    type="button"
                    className="text-blue-600 hover:text-blue-800 leading-none"
                    onClick={() => removePinnedConn(cid)}
                    aria-label="Remover"
                  >
                    ×
                  </button>
                </span>
              );
            })}
            {f.code_refs.tool_slugs.map((slug) => (
              <span
                key={slug}
                className="inline-flex items-center gap-1 bg-emerald-50 border border-emerald-200 rounded px-2 py-0.5"
              >
                <span className="font-mono">#{slug}</span>
                <button
                  type="button"
                  className="text-emerald-700 hover:text-emerald-900 leading-none"
                  onClick={() => removePinnedTool(slug)}
                  aria-label="Remover"
                >
                  ×
                </button>
              </span>
            ))}
            {f.code_refs.connection_ids.length === 0 &&
              f.code_refs.tool_slugs.length === 0 && (
                <span className="text-muted-foreground italic">
                  vazio — adicione fontes (@) e tools (#) para o assistente entender seu domínio
                </span>
              )}
            <div className="ml-auto relative">
              <button
                type="button"
                className="px-2 py-0.5 border border-border rounded hover:bg-muted"
                onClick={() => setCtxPickerOpen((v) => !v)}
              >
                + Adicionar
              </button>
              {ctxPickerOpen && (
                <div className="absolute bottom-full right-0 mb-1 bg-white border border-border rounded shadow-lg max-h-72 overflow-y-auto min-w-[240px] z-30">
                  <div className="px-3 py-1 text-xs font-semibold text-muted-foreground border-b border-border">
                    Fontes de dados
                  </div>
                  {(conns.data || [])
                    .filter(
                      (c: any) => !f.code_refs.connection_ids.includes(c.id),
                    )
                    .map((c: any) => (
                      <button
                        key={c.id}
                        type="button"
                        className="w-full text-left px-3 py-1 text-xs hover:bg-muted block"
                        onClick={() => {
                          addPinnedConn(c.id);
                          loadConnSchema(c.name);
                          setCtxPickerOpen(false);
                        }}
                      >
                        <span className="font-mono">@{c.name}</span>{" "}
                        <span className="text-muted-foreground">({c.type})</span>
                      </button>
                    ))}
                  {(conns.data || []).filter(
                    (c: any) => !f.code_refs.connection_ids.includes(c.id),
                  ).length === 0 && (
                    <div className="px-3 py-1 text-xs text-muted-foreground italic">
                      (todas já fixadas)
                    </div>
                  )}
                  <div className="px-3 py-1 text-xs font-semibold text-muted-foreground border-y border-border">
                    Tools
                  </div>
                  {(tools.data || [])
                    .filter(
                      (t: any) =>
                        !f.code_refs.tool_slugs.includes(t.slug) &&
                        t.slug !== f.slug,
                    )
                    .map((t: any) => (
                      <button
                        key={t.id}
                        type="button"
                        className="w-full text-left px-3 py-1 text-xs hover:bg-muted block"
                        onClick={() => {
                          addPinnedTool(t.slug);
                          setCtxPickerOpen(false);
                        }}
                      >
                        <span className="font-mono">#{t.slug}</span>{" "}
                        <span className="text-muted-foreground">— {t.title}</span>
                      </button>
                    ))}
                </div>
              )}
            </div>
          </div>

          <form onSubmit={submitChat} className="border-t border-border p-3 flex gap-2">
            <div className="flex-1 relative">
              {mentionMenu && (mentionMenu.loading || mentionMenu.items.length > 0) && (
                <div className="absolute bottom-full left-0 right-0 mb-1 bg-white border border-border rounded shadow-lg max-h-56 overflow-y-auto z-20">
                  {mentionMenu.loading && (
                    <div className="px-3 py-2 text-xs text-muted-foreground">
                      Carregando...
                    </div>
                  )}
                  {!mentionMenu.loading &&
                    mentionMenu.items.map((it, idx) => (
                      <button
                        type="button"
                        key={it.insert + "-" + idx}
                        className={
                          "w-full text-left px-3 py-1.5 text-xs hover:bg-muted block " +
                          (idx === mentionMenu.activeIdx ? "bg-muted" : "")
                        }
                        onMouseDown={(e) => {
                          e.preventDefault();
                          applyMention(it);
                        }}
                        onMouseEnter={() =>
                          setMentionMenu((m) => (m ? { ...m, activeIdx: idx } : m))
                        }
                      >
                        <span className="font-mono">
                          {mentionMenu.trigger}
                          {it.label}
                        </span>
                        {it.sub && (
                          <span className="text-muted-foreground ml-2">
                            — {it.sub}
                          </span>
                        )}
                      </button>
                    ))}
                </div>
              )}
              <textarea
                ref={chatInputRef}
                className="w-full border border-border rounded px-3 py-2 text-sm h-12 resize-none"
                placeholder={
                  f.kind === "code"
                    ? "Descreva o que a tool deve fazer (use @ para dados, # para tools)..."
                    : f.connection_id
                    ? "Descreva o que você precisa (use @ para tabelas, # para tools)..."
                    : "Selecione uma connection no formulário primeiro"
                }
                value={chatInput}
                onChange={(e) =>
                  onChatInputChange(e.target.value, e.target.selectionStart || 0)
                }
                onSelect={(e) => {
                  const t = e.currentTarget;
                  const hit = detectMention(t.value, t.selectionStart || 0);
                  if (!hit) setMentionMenu(null);
                }}
                onKeyDown={(e) => {
                  if (mentionMenu && (mentionMenu.loading || mentionMenu.items.length > 0)) {
                    if (e.key === "ArrowDown") {
                      e.preventDefault();
                      setMentionMenu((m) =>
                        m
                          ? {
                              ...m,
                              activeIdx: Math.min(
                                m.activeIdx + 1,
                                Math.max(0, m.items.length - 1),
                              ),
                            }
                          : m,
                      );
                      return;
                    }
                    if (e.key === "ArrowUp") {
                      e.preventDefault();
                      setMentionMenu((m) =>
                        m ? { ...m, activeIdx: Math.max(0, m.activeIdx - 1) } : m,
                      );
                      return;
                    }
                    if (e.key === "Enter" || e.key === "Tab") {
                      if (!mentionMenu.loading && mentionMenu.items.length > 0) {
                        e.preventDefault();
                        applyMention(mentionMenu.items[mentionMenu.activeIdx]);
                        return;
                      }
                    }
                    if (e.key === "Escape") {
                      e.preventDefault();
                      setMentionMenu(null);
                      return;
                    }
                  }
                  if (e.key === "Enter" && !e.shiftKey) {
                    e.preventDefault();
                    submitChat();
                  }
                }}
                disabled={
                  (f.kind === "query" && !f.connection_id) ||
                  send.isPending ||
                  generateCode.isPending ||
                  fixError.isPending
                }
              />
            </div>
            <button
              type="submit"
              className="bg-primary text-white rounded px-4 disabled:opacity-50"
              disabled={
                (f.kind === "query" && !f.connection_id) ||
                !chatInput.trim() ||
                send.isPending ||
                generateCode.isPending ||
                fixError.isPending
              }
            >
              {f.kind === "code" ? "Gerar" : "Enviar"}
            </button>
          </form>
        </div>
      </div>

      {/* ============== Existing tools table ============== */}
      <div className="border border-border rounded bg-white">
        <div className="flex items-center justify-between gap-2 p-2 border-b border-border">
          <input
            className="border border-border rounded px-2 py-1 text-sm w-full max-w-xs"
            placeholder="Buscar por slug, título ou descrição..."
            value={tableSearch}
            onChange={(e) => setTableSearch(e.target.value)}
          />
          <span className="text-xs text-muted-foreground whitespace-nowrap">
            {toolsPage.data ? `${pageTotal} tool(s)` : ""}
          </span>
        </div>
        <div className="overflow-x-auto">
        <table className="w-full text-sm min-w-[960px]">
          <thead className="bg-muted">
            <tr>
              <th className="text-left p-2">Slug</th>
              <th className="text-left p-2">Título</th>
              <th className="text-left p-2">Tipo</th>
              <th className="text-left p-2">Grupo</th>
              <th className="text-left p-2">Status</th>
              <th className="text-left p-2">Versão</th>
              <th className="text-left p-2">Cache</th>
              <th className="text-left p-2 whitespace-nowrap">Criado em</th>
              <th className="text-left p-2 whitespace-nowrap">Atualizado em</th>
              <th className="text-right p-2">Ações</th>
            </tr>
          </thead>
          <tbody>
            {pageItems.map((t) => {
                const kind = (t.kind || "query").toLowerCase();
                const status = (t.status || "").toLowerCase();
                const statusVariant =
                  status === "active" || status === "enabled"
                    ? "success"
                    : status === "disabled" || status === "draft"
                    ? "muted"
                    : "default";
                return (
              <tr key={t.id} className={"border-t border-border " + (editingId === t.id ? "bg-blue-50" : "")}>
                <td className="p-2 font-mono">{t.slug}</td>
                <td className="p-2">{t.title || <span className="text-muted-foreground">—</span>}</td>
                <td className="p-2">
                  <Badge variant={kind === "code" ? "info" : "default"}>{kind}</Badge>
                </td>
                <td className="p-2">
                  {(() => {
                    const g = groups.data?.find((x: any) => x.id === t.group_id);
                    return g ? <span className="text-slate-700">{g.name}</span> : <span className="text-muted-foreground">—</span>;
                  })()}
                </td>
                <td className="p-2">
                  <Badge variant={statusVariant as any}>{t.status || "—"}</Badge>
                </td>
                <td className="p-2">{t.version ?? "—"}</td>
                <td className="p-2 whitespace-nowrap">{t.cache_ttl_sec}s</td>
                <td className="p-2 whitespace-nowrap text-xs text-slate-600" title={fmtDate(t.created_at)}>
                  {fmtDate(t.created_at, { short: true })}
                </td>
                <td className="p-2 whitespace-nowrap text-xs text-slate-600" title={fmtDate(t.updated_at)}>
                  {fmtDate(t.updated_at, { short: true })}
                </td>
                <td className="p-2 text-right whitespace-nowrap space-x-3">
                  <button
                    className="text-emerald-600 text-xs hover:underline"
                    onClick={() => openTest(t)}
                  >
                    Testar
                  </button>
                  <button
                    className="text-blue-600 text-xs hover:underline"
                    onClick={() => startEdit(t.id)}
                  >
                    Editar
                  </button>
                  <button
                    className="text-gray-600 text-xs hover:underline"
                    onClick={() => invalidate.mutate(t.id)}
                  >
                    Invalidar cache
                  </button>
                  <button
                    className="text-red-600 text-xs hover:underline"
                    onClick={() => {
                      if (
                        window.confirm(
                          `Excluir a tool "${t.slug}"?\n\nEsta ação é permanente e também remove todos os acessos individuais a ela.`,
                        )
                      )
                        remove.mutate(t.id);
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
        {pageTotal > 0 && (
          <Pagination
            page={page}
            totalPages={totalPages}
            total={pageTotal}
            start={pageStart}
            end={pageEnd}
            pageSize={pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        )}
        {toolsPage.data && pageTotal === 0 && !debouncedSearch && (
          <EmptyState
            title="Nenhuma tool cadastrada"
            description="Use o formulário acima para criar sua primeira tool."
          />
        )}
        {toolsPage.data && pageTotal === 0 && debouncedSearch && (
          <EmptyState
            title="Nenhuma tool corresponde ao filtro"
            description="Tente outro termo de busca."
          />
        )}
      </div>

      {testTool && (
        <div
          className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center p-4"
          onClick={() => setTestTool(null)}
        >
          <div
            className="bg-white rounded shadow-xl max-w-2xl w-full max-h-[90vh] overflow-y-auto"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between border-b border-border px-4 py-3">
              <div>
                <div className="font-semibold">Testar tool</div>
                <div className="text-xs text-muted-foreground font-mono">{testTool.slug}</div>
              </div>
              <button
                type="button"
                className="text-sm px-2 py-1 hover:bg-muted rounded"
                onClick={() => setTestTool(null)}
              >
                Fechar
              </button>
            </div>

            <div className="p-4 space-y-3">
              {Object.keys(testTool._props || {}).length === 0 ? (
                <div className="text-sm text-muted-foreground">
                  Esta tool não declara parâmetros.
                </div>
              ) : (
                <div className="space-y-2">
                  {Object.entries(testTool._props as Record<string, any>).map(([name, spec]) => {
                    const ty = (spec?.type || "string").toLowerCase();
                    const required = (testTool._schema?.required || []).includes(name);
                    return (
                      <div key={name} className="space-y-1">
                        <label className="text-xs font-medium">
                          <code>{name}</code>{" "}
                          <span className="text-muted-foreground">({ty}{required ? ", obrigatório" : ""})</span>
                          {spec?.description && (
                            <span className="text-muted-foreground"> — {spec.description}</span>
                          )}
                        </label>
                        {ty === "boolean" ? (
                          <input
                            type="checkbox"
                            checked={!!testParams[name]}
                            onChange={(e) => setTestParams((p) => ({ ...p, [name]: e.target.checked }))}
                          />
                        ) : ty === "array" || ty === "object" ? (
                          <textarea
                            className="w-full border border-border rounded px-2 py-1 text-xs font-mono h-20"
                            placeholder={ty === "array" ? "[1, 2, 3]" : '{"key": "value"}'}
                            value={testParams[name] ?? ""}
                            onChange={(e) => setTestParams((p) => ({ ...p, [name]: e.target.value }))}
                          />
                        ) : (
                          <input
                            type={ty === "number" || ty === "integer" ? "number" : "text"}
                            className="w-full border border-border rounded px-2 py-1 text-sm"
                            value={testParams[name] ?? ""}
                            onChange={(e) => setTestParams((p) => ({ ...p, [name]: e.target.value }))}
                          />
                        )}
                      </div>
                    );
                  })}
                </div>
              )}

              <div className="flex gap-2 pt-2">
                <button
                  type="button"
                  className="bg-primary text-white rounded px-4 py-2 text-sm disabled:opacity-50"
                  onClick={runTest}
                  disabled={testRunning}
                >
                  {testRunning ? "Executando..." : "Executar"}
                </button>
                <button
                  type="button"
                  className="text-sm border border-border rounded px-4 py-2 hover:bg-muted"
                  onClick={() => { setTestResult(null); setTestError(null); }}
                >
                  Limpar resultado
                </button>
              </div>

              {testError && (
                <div className="bg-red-50 border border-red-200 text-red-700 rounded p-3 text-sm whitespace-pre-wrap">
                  {testError}
                </div>
              )}
              {testResult && (
                <div className="space-y-2">
                  <div className="text-xs text-muted-foreground">
                    Resultado ({(testResult.rows || testResult.Rows || []).length} linha(s))
                  </div>
                  <pre className="font-mono text-xs bg-muted rounded p-3 overflow-x-auto max-h-80">
{JSON.stringify(testResult, null, 2)}
                  </pre>
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
