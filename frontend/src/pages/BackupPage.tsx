import { useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, apiBlob, getAuth } from "../auth";
import { EmptyState } from "../components/EmptyState";

type Connection = { id: string; name: string; type: string; description?: string };
type Group = { id: string; name: string; description?: string };
type Tool = { id: string; slug: string; title?: string; group_id: string };

type ConflictAction = "skip" | "overwrite" | "duplicate";
type ItemStatus = "new" | "conflict_id" | "conflict_unique" | "missing_dependency";

type PreviewItem = {
  type: "connection" | "group" | "tool";
  id: string;
  name: string;
  status: ItemStatus;
  existing_id?: string;
  suggested_action: ConflictAction;
  reason?: string;
};

type Preview = {
  manifest: {
    exported_at: string;
    exported_by?: string;
    source_master_key_fingerprint: string;
  };
  items: PreviewItem[];
  counts: Record<string, number>;
};

type ItemReport = {
  type: string;
  id: string;
  new_id?: string;
  name: string;
  action: ConflictAction;
  status: "created" | "updated" | "skipped" | "error";
  message?: string;
};

type Report = {
  items: ItemReport[];
  counts: Record<string, number>;
};

const STATUS_LABEL: Record<ItemStatus, string> = {
  new: "Novo",
  conflict_id: "Conflito por ID",
  conflict_unique: "Conflito por nome/slug",
  missing_dependency: "Dependência ausente",
};

const ACTION_LABEL: Record<ConflictAction, string> = {
  skip: "Pular",
  overwrite: "Sobrescrever",
  duplicate: "Duplicar",
};

const ROLE_ALLOWED = new Set(["admin", "editor"]);

export function BackupPage() {
  const auth = getAuth();
  const allowed = !!auth && ROLE_ALLOWED.has(auth.role);
  const [tab, setTab] = useState<"export" | "restore">("export");

  if (!allowed) {
    return (
      <div className="p-6">
        <h2 className="text-xl font-bold mb-2">Backup e restauração</h2>
        <p className="text-sm text-slate-600">
          Você precisa do papel <strong>admin</strong> ou <strong>editor</strong> para usar esta área.
        </p>
      </div>
    );
  }

  return (
    <div className="p-4 md:p-6 space-y-4">
      <div>
        <h2 className="text-xl font-bold">Backup e restauração</h2>
        <p className="text-sm text-slate-600">
          Exporta conexões, grupos e tools (com histórico de versões) em um arquivo criptografado pela
          chave-mestra deste servidor. O arquivo só pode ser restaurado em uma instância com a mesma{" "}
          <code>MASTER_KEY</code>.
        </p>
      </div>
      <div className="flex gap-2 border-b border-border">
        <button
          className={
            "px-4 py-2 text-sm border-b-2 -mb-px " +
            (tab === "export" ? "border-primary text-primary font-medium" : "border-transparent text-slate-600")
          }
          onClick={() => setTab("export")}
        >
          Exportar
        </button>
        <button
          className={
            "px-4 py-2 text-sm border-b-2 -mb-px " +
            (tab === "restore" ? "border-primary text-primary font-medium" : "border-transparent text-slate-600")
          }
          onClick={() => setTab("restore")}
        >
          Restaurar
        </button>
      </div>
      {tab === "export" ? <ExportPanel /> : <RestorePanel />}
    </div>
  );
}

// ============================================================================
// Export panel
// ============================================================================

function ExportPanel() {
  const connections = useQuery({
    queryKey: ["backup-connections"],
    queryFn: () => api<Connection[]>("/api/connections"),
  });
  const groups = useQuery({
    queryKey: ["backup-groups"],
    queryFn: () => api<Group[]>("/api/groups"),
  });
  const tools = useQuery({
    queryKey: ["backup-tools"],
    queryFn: () => api<Tool[]>("/api/tools"),
  });

  const [global, setGlobal] = useState(true);
  const [connSel, setConnSel] = useState<Set<string>>(new Set());
  const [groupSel, setGroupSel] = useState<Set<string>>(new Set());
  const [toolSel, setToolSel] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const connFiltered = connections.data ?? [];
  const groupFiltered = groups.data ?? [];
  const toolFiltered = tools.data ?? [];

  const toggle = (s: Set<string>, setS: (n: Set<string>) => void, id: string) => {
    const n = new Set(s);
    if (n.has(id)) n.delete(id);
    else n.add(id);
    setS(n);
  };
  const toggleAll = (items: { id: string }[], setS: (n: Set<string>) => void) => {
    setS(new Set(items.map((x) => x.id)));
  };
  const clearAll = (setS: (n: Set<string>) => void) => setS(new Set());

  const totalSelected = connSel.size + groupSel.size + toolSel.size;
  const canExport = global || totalSelected > 0;

  const handleExport = async () => {
    setErr(null);
    setDone(null);
    setBusy(true);
    try {
      const body = global
        ? { global: true }
        : {
            global: false,
            connection_ids: [...connSel],
            group_ids: [...groupSel],
            tool_ids: [...toolSel],
          };
      const blob = await apiBlob("/api/backup/export", {
        method: "POST",
        body: JSON.stringify(body),
      });
      const filename = `contextforge-${new Date()
        .toISOString()
        .replace(/[-:]/g, "")
        .replace(/\..+/, "")}.mcpbak`;
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      setDone(`Backup gerado (${(blob.size / 1024).toFixed(1)} KiB).`);
    } catch (e: any) {
      setErr(e?.message || "Falha ao gerar backup");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="border border-border rounded p-4 bg-white">
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="radio"
            name="scope"
            checked={global}
            onChange={() => setGlobal(true)}
          />
          <span className="text-sm font-medium">Backup global</span>
          <span className="text-xs text-slate-500">— inclui todas as conexões, grupos e tools</span>
        </label>
        <label className="flex items-center gap-2 cursor-pointer mt-2">
          <input
            type="radio"
            name="scope"
            checked={!global}
            onChange={() => setGlobal(false)}
          />
          <span className="text-sm font-medium">Seleção personalizada</span>
          <span className="text-xs text-slate-500">
            — escolha itens individualmente. Dependências (grupos/conexões usados por tools) são incluídas automaticamente.
          </span>
        </label>
      </div>

      {!global && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <SelectableList
            title="Conexões"
            items={connFiltered.map((c) => ({ id: c.id, label: c.name, sub: c.type }))}
            selected={connSel}
            onToggle={(id) => toggle(connSel, setConnSel, id)}
            onAll={() => toggleAll(connFiltered, setConnSel)}
            onNone={() => clearAll(setConnSel)}
          />
          <SelectableList
            title="Grupos"
            items={groupFiltered.map((g) => ({ id: g.id, label: g.name, sub: g.description }))}
            selected={groupSel}
            onToggle={(id) => toggle(groupSel, setGroupSel, id)}
            onAll={() => toggleAll(groupFiltered, setGroupSel)}
            onNone={() => clearAll(setGroupSel)}
          />
          <SelectableList
            title="Tools"
            items={toolFiltered.map((t) => ({ id: t.id, label: t.slug, sub: t.title }))}
            selected={toolSel}
            onToggle={(id) => toggle(toolSel, setToolSel, id)}
            onAll={() => toggleAll(toolFiltered, setToolSel)}
            onNone={() => clearAll(setToolSel)}
          />
        </div>
      )}

      <div className="flex items-center gap-3">
        <button
          className="px-4 py-2 rounded bg-primary text-white text-sm disabled:opacity-50"
          disabled={!canExport || busy}
          onClick={handleExport}
        >
          {busy ? "Gerando…" : "Gerar backup"}
        </button>
        {!global && (
          <span className="text-xs text-slate-500">
            {totalSelected} item(ns) selecionado(s)
          </span>
        )}
        {done && <span className="text-sm text-green-700">{done}</span>}
        {err && <span className="text-sm text-red-700">{err}</span>}
      </div>
    </div>
  );
}

function SelectableList({
  title,
  items,
  selected,
  onToggle,
  onAll,
  onNone,
}: {
  title: string;
  items: { id: string; label: string; sub?: string }[];
  selected: Set<string>;
  onToggle: (id: string) => void;
  onAll: () => void;
  onNone: () => void;
}) {
  const [q, setQ] = useState("");
  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase();
    if (!t) return items;
    return items.filter(
      (i) => i.label.toLowerCase().includes(t) || (i.sub || "").toLowerCase().includes(t),
    );
  }, [q, items]);
  return (
    <div className="border border-border rounded bg-white flex flex-col min-h-[200px]">
      <div className="px-3 py-2 border-b border-border flex items-center justify-between">
        <strong className="text-sm">{title}</strong>
        <span className="text-xs text-slate-500">
          {selected.size}/{items.length}
        </span>
      </div>
      <div className="px-3 py-2 flex gap-2 border-b border-border">
        <input
          className="border border-border rounded px-2 py-1 text-xs flex-1"
          placeholder="Buscar…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <button className="text-xs text-primary hover:underline" onClick={onAll}>
          todos
        </button>
        <button className="text-xs text-slate-500 hover:underline" onClick={onNone}>
          nenhum
        </button>
      </div>
      <div className="overflow-auto max-h-72 flex-1">
        {filtered.length === 0 ? (
          <div className="p-3 text-xs text-slate-500">Nenhum item.</div>
        ) : (
          <ul>
            {filtered.map((i) => (
              <li
                key={i.id}
                className="flex items-start gap-2 px-3 py-1.5 hover:bg-muted cursor-pointer"
                onClick={() => onToggle(i.id)}
              >
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={selected.has(i.id)}
                  onChange={() => onToggle(i.id)}
                  onClick={(e) => e.stopPropagation()}
                />
                <div className="min-w-0 flex-1">
                  <div className="text-sm truncate">{i.label}</div>
                  {i.sub && (
                    <div className="text-xs text-slate-500 truncate">{i.sub}</div>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

// ============================================================================
// Restore panel
// ============================================================================

function RestorePanel() {
  const inputRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [resolutions, setResolutions] = useState<Record<string, ConflictAction>>({});
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [confirming, setConfirming] = useState(false);

  const reset = () => {
    setFile(null);
    setPreview(null);
    setResolutions({});
    setReport(null);
    setErr(null);
    if (inputRef.current) inputRef.current.value = "";
  };

  const doPreview = async () => {
    if (!file) return;
    setErr(null);
    setReport(null);
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const p = await api<Preview>("/api/backup/preview", { method: "POST", body: fd });
      setPreview(p);
      // Seed resolutions with suggested actions.
      const seed: Record<string, ConflictAction> = {};
      p.items.forEach((it) => {
        seed[it.type + ":" + it.id] = it.suggested_action;
      });
      setResolutions(seed);
    } catch (e: any) {
      setErr(e?.message || "Falha na pré-visualização");
    } finally {
      setBusy(false);
    }
  };

  const setAction = (it: PreviewItem, a: ConflictAction) => {
    setResolutions((prev) => ({ ...prev, [it.type + ":" + it.id]: a }));
  };

  const setBulkAction = (type: PreviewItem["type"], a: ConflictAction) => {
    if (!preview) return;
    setResolutions((prev) => {
      const next = { ...prev };
      preview.items.forEach((it) => {
        if (it.type === type && it.status !== "missing_dependency") {
          next[it.type + ":" + it.id] = a;
        }
      });
      return next;
    });
  };

  const doRestore = async () => {
    if (!file || !preview) return;
    setErr(null);
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const arr = preview.items.map((it) => ({
        type: it.type,
        id: it.id,
        action: resolutions[it.type + ":" + it.id] || it.suggested_action,
      }));
      fd.append("resolutions", JSON.stringify(arr));
      const rep = await api<Report>("/api/backup/restore", { method: "POST", body: fd });
      setReport(rep);
      setPreview(null);
    } catch (e: any) {
      setErr(e?.message || "Falha na restauração");
    } finally {
      setBusy(false);
      setConfirming(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="border border-border rounded p-4 bg-white">
        <div className="flex items-center gap-3 flex-wrap">
          <input
            ref={inputRef}
            type="file"
            accept=".mcpbak,application/octet-stream"
            className="text-sm"
            onChange={(e) => {
              const f = e.target.files?.[0] || null;
              setFile(f);
              setPreview(null);
              setReport(null);
              setErr(null);
            }}
          />
          <button
            className="px-3 py-1.5 rounded bg-primary text-white text-sm disabled:opacity-50"
            disabled={!file || busy}
            onClick={doPreview}
          >
            Pré-visualizar
          </button>
          {file && (
            <button className="text-xs text-slate-500 hover:underline" onClick={reset}>
              limpar
            </button>
          )}
        </div>
        {err && <div className="mt-2 text-sm text-red-700">{err}</div>}
      </div>

      {preview && (
        <RestorePreview
          preview={preview}
          resolutions={resolutions}
          setAction={setAction}
          setBulkAction={setBulkAction}
          onApply={() => setConfirming(true)}
          busy={busy}
        />
      )}

      {confirming && (
        <ConfirmModal
          onCancel={() => setConfirming(false)}
          onConfirm={doRestore}
          busy={busy}
        />
      )}

      {report && <RestoreReport report={report} />}
    </div>
  );
}

function RestorePreview({
  preview,
  resolutions,
  setAction,
  setBulkAction,
  onApply,
  busy,
}: {
  preview: Preview;
  resolutions: Record<string, ConflictAction>;
  setAction: (it: PreviewItem, a: ConflictAction) => void;
  setBulkAction: (type: PreviewItem["type"], a: ConflictAction) => void;
  onApply: () => void;
  busy: boolean;
}) {
  const byType = {
    connection: preview.items.filter((i) => i.type === "connection"),
    group: preview.items.filter((i) => i.type === "group"),
    tool: preview.items.filter((i) => i.type === "tool"),
  };

  return (
    <div className="space-y-4">
      <div className="border border-border rounded p-3 bg-muted text-xs space-y-1">
        <div>
          <strong>Exportado em:</strong>{" "}
          {new Date(preview.manifest.exported_at).toLocaleString()}
        </div>
        <div>
          <strong>Conteúdo:</strong> {preview.counts.connections} conexões ·{" "}
          {preview.counts.groups} grupos · {preview.counts.tools} tools ·{" "}
          {preview.counts.tool_versions} versões
        </div>
      </div>

      {(["connection", "group", "tool"] as const).map((type) => (
        <PreviewSection
          key={type}
          type={type}
          items={byType[type]}
          resolutions={resolutions}
          setAction={setAction}
          setBulkAction={setBulkAction}
        />
      ))}

      <div className="flex justify-end">
        <button
          className="px-4 py-2 rounded bg-primary text-white text-sm disabled:opacity-50"
          disabled={busy}
          onClick={onApply}
        >
          Aplicar restauração
        </button>
      </div>
    </div>
  );
}

function PreviewSection({
  type,
  items,
  resolutions,
  setAction,
  setBulkAction,
}: {
  type: PreviewItem["type"];
  items: PreviewItem[];
  resolutions: Record<string, ConflictAction>;
  setAction: (it: PreviewItem, a: ConflictAction) => void;
  setBulkAction: (type: PreviewItem["type"], a: ConflictAction) => void;
}) {
  const label =
    type === "connection" ? "Conexões" : type === "group" ? "Grupos" : "Tools";
  if (items.length === 0) {
    return (
      <div className="border border-border rounded bg-white">
        <div className="px-3 py-2 border-b border-border flex items-center justify-between">
          <strong className="text-sm">{label}</strong>
          <span className="text-xs text-slate-500">vazio</span>
        </div>
      </div>
    );
  }
  return (
    <div className="border border-border rounded bg-white">
      <div className="px-3 py-2 border-b border-border flex items-center justify-between flex-wrap gap-2">
        <strong className="text-sm">
          {label} <span className="text-slate-500 font-normal">({items.length})</span>
        </strong>
        <div className="flex items-center gap-1 text-xs">
          <span className="text-slate-500 mr-1">aplicar a todos:</span>
          <button
            className="px-2 py-0.5 border border-border rounded hover:bg-muted"
            onClick={() => setBulkAction(type, "skip")}
          >
            Pular
          </button>
          <button
            className="px-2 py-0.5 border border-border rounded hover:bg-muted"
            onClick={() => setBulkAction(type, "overwrite")}
          >
            Sobrescrever
          </button>
          <button
            className="px-2 py-0.5 border border-border rounded hover:bg-muted"
            onClick={() => setBulkAction(type, "duplicate")}
          >
            Duplicar
          </button>
        </div>
      </div>
      <div className="overflow-auto">
        <table className="w-full text-sm">
          <thead className="bg-muted text-left text-xs">
            <tr>
              <th className="px-3 py-2">Nome / slug</th>
              <th className="px-3 py-2">Status</th>
              <th className="px-3 py-2">Ação</th>
            </tr>
          </thead>
          <tbody>
            {items.map((it) => (
              <tr key={it.id} className="border-t border-border">
                <td className="px-3 py-2">
                  <div className="font-mono text-xs truncate">{it.name}</div>
                  {it.reason && (
                    <div className="text-xs text-slate-500">{it.reason}</div>
                  )}
                </td>
                <td className="px-3 py-2">
                  <span
                    className={
                      "text-xs px-2 py-0.5 rounded " +
                      (it.status === "new"
                        ? "bg-green-100 text-green-800"
                        : it.status === "missing_dependency"
                        ? "bg-red-100 text-red-800"
                        : "bg-amber-100 text-amber-800")
                    }
                  >
                    {STATUS_LABEL[it.status]}
                  </span>
                </td>
                <td className="px-3 py-2">
                  {it.status === "missing_dependency" ? (
                    <span className="text-xs text-slate-500">— sempre pulado</span>
                  ) : (
                    <select
                      className="border border-border rounded px-2 py-1 text-xs"
                      value={
                        resolutions[it.type + ":" + it.id] || it.suggested_action
                      }
                      onChange={(e) =>
                        setAction(it, e.target.value as ConflictAction)
                      }
                    >
                      {it.status === "new" ? (
                        <>
                          <option value="overwrite">Criar</option>
                          <option value="skip">Pular</option>
                        </>
                      ) : (
                        <>
                          <option value="skip">{ACTION_LABEL.skip}</option>
                          <option value="overwrite">
                            {ACTION_LABEL.overwrite}
                          </option>
                          <option value="duplicate">{ACTION_LABEL.duplicate}</option>
                        </>
                      )}
                    </select>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ConfirmModal({
  onCancel,
  onConfirm,
  busy,
}: {
  onCancel: () => void;
  onConfirm: () => void;
  busy: boolean;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-white rounded shadow-lg max-w-md w-full p-5 space-y-3">
        <h3 className="font-bold text-lg">Confirmar restauração</h3>
        <p className="text-sm text-slate-700">
          Esta operação modifica dados em produção e roda em uma única transação.
          Itens marcados como <strong>sobrescrever</strong> substituirão registros
          existentes. <strong>Não há undo automático.</strong>
        </p>
        <div className="flex justify-end gap-2 pt-2">
          <button
            className="px-3 py-1.5 rounded border border-border text-sm"
            onClick={onCancel}
            disabled={busy}
          >
            Cancelar
          </button>
          <button
            className="px-3 py-1.5 rounded bg-primary text-white text-sm disabled:opacity-50"
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? "Aplicando…" : "Aplicar restauração"}
          </button>
        </div>
      </div>
    </div>
  );
}

function RestoreReport({ report }: { report: Report }) {
  const c = report.counts || {};
  return (
    <div className="border border-border rounded bg-white">
      <div className="px-3 py-2 border-b border-border flex items-center justify-between">
        <strong className="text-sm">Resultado da restauração</strong>
        <span className="text-xs text-slate-500">
          {c.created || 0} criados · {c.updated || 0} atualizados ·{" "}
          {c.duplicated || 0} duplicados · {c.skipped || 0} pulados ·{" "}
          {c.errors || 0} erros
        </span>
      </div>
      {report.items.length === 0 ? (
        <EmptyState title="Nenhum item processado." />
      ) : (
        <div className="overflow-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted text-left text-xs">
              <tr>
                <th className="px-3 py-2">Tipo</th>
                <th className="px-3 py-2">Nome</th>
                <th className="px-3 py-2">Ação</th>
                <th className="px-3 py-2">Status</th>
                <th className="px-3 py-2">Mensagem</th>
              </tr>
            </thead>
            <tbody>
              {report.items.map((it, idx) => (
                <tr key={idx} className="border-t border-border">
                  <td className="px-3 py-2 text-xs">{it.type}</td>
                  <td className="px-3 py-2 font-mono text-xs">{it.name}</td>
                  <td className="px-3 py-2 text-xs">{ACTION_LABEL[it.action] || it.action}</td>
                  <td className="px-3 py-2 text-xs">
                    <span
                      className={
                        "px-2 py-0.5 rounded " +
                        (it.status === "created" || it.status === "updated"
                          ? "bg-green-100 text-green-800"
                          : it.status === "skipped"
                          ? "bg-slate-100 text-slate-700"
                          : "bg-red-100 text-red-800")
                      }
                    >
                      {it.status}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-xs text-slate-600">{it.message || ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
