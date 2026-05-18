import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, getAuth } from "../auth";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { fmtDate } from "../utils/format";

type Role = "admin" | "editor" | "viewer";
type User = {
  id: string;
  email: string;
  role: Role;
  disabled: boolean;
  created_at?: string;
  updated_at?: string;
};

export function UsersPage() {
  const qc = useQueryClient();
  const auth = getAuth();
  const meId = auth?.user_id || "";
  const isAdmin = auth?.role === "admin";
  const [editing, setEditing] = useState<User | null>(null);
  const [pwTarget, setPwTarget] = useState<User | null>(null);
  const [creating, setCreating] = useState(false);

  const users = useQuery({
    queryKey: ["users"],
    queryFn: () => api<User[]>("/api/users"),
    enabled: isAdmin,
  });

  const remove = useMutation({
    mutationFn: (id: string) => api(`/api/users/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });

  if (!isAdmin) {
    return (
      <div className="max-w-xl">
        <h1 className="text-2xl font-bold mb-2">Usuários</h1>
        <div className="rounded border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
          Esta seção é restrita a administradores.
        </div>
      </div>
    );
  }

  const list = users.data ?? [];

  return (
    <div className="max-w-5xl">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-bold">Usuários</h1>
          <p className="text-sm text-slate-500">
            Gerencie contas administrativas e suas permissões.
          </p>
        </div>
        <button
          className="bg-primary text-white px-3 py-2 rounded text-sm"
          onClick={() => setCreating(true)}
        >
          Novo usuário
        </button>
      </div>

      <div className="border border-border rounded bg-white">
        {users.isLoading ? (
          <div className="p-6 text-sm text-slate-500">Carregando…</div>
        ) : list.length === 0 ? (
          <EmptyState title="Nenhum usuário cadastrado." />
        ) : (
          <table className="w-full text-sm">
            <thead className="bg-muted text-left">
              <tr>
                <th className="px-3 py-2">Email</th>
                <th className="px-3 py-2">Papel</th>
                <th className="px-3 py-2">Status</th>
                <th className="px-3 py-2">Criado em</th>
                <th className="px-3 py-2 text-right">Ações</th>
              </tr>
            </thead>
            <tbody>
              {list.map((u) => (
                <tr key={u.id} className="border-t border-border">
                  <td className="px-3 py-2">
                    <div className="font-medium">{u.email}</div>
                    {u.id === meId && (
                      <div className="text-xs text-slate-500">(você)</div>
                    )}
                  </td>
                  <td className="px-3 py-2">
                    <RoleBadge role={u.role} />
                  </td>
                  <td className="px-3 py-2">
                    {u.disabled ? (
                      <Badge variant="danger">desativado</Badge>
                    ) : (
                      <Badge variant="success">ativo</Badge>
                    )}
                  </td>
                  <td className="px-3 py-2 text-slate-500">{fmtDate(u.created_at)}</td>
                  <td className="px-3 py-2 text-right whitespace-nowrap">
                    <button
                      className="text-xs px-2 py-1 hover:bg-muted rounded"
                      onClick={() => setEditing(u)}
                    >
                      Editar
                    </button>
                    <button
                      className="text-xs px-2 py-1 hover:bg-muted rounded"
                      onClick={() => setPwTarget(u)}
                    >
                      Senha
                    </button>
                    <button
                      className="text-xs px-2 py-1 hover:bg-red-50 text-red-700 rounded disabled:opacity-40"
                      disabled={u.id === meId}
                      onClick={() => {
                        if (confirm(`Excluir o usuário ${u.email}?`)) remove.mutate(u.id);
                      }}
                    >
                      Excluir
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {creating && (
        <CreateUserModal
          onClose={() => setCreating(false)}
          onSaved={() => {
            setCreating(false);
            qc.invalidateQueries({ queryKey: ["users"] });
          }}
        />
      )}
      {editing && (
        <EditUserModal
          user={editing}
          isSelf={editing.id === meId}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            qc.invalidateQueries({ queryKey: ["users"] });
          }}
        />
      )}
      {pwTarget && (
        <PasswordModal
          user={pwTarget}
          onClose={() => setPwTarget(null)}
          onSaved={() => setPwTarget(null)}
        />
      )}
    </div>
  );
}

function RoleBadge({ role }: { role: Role }) {
  const v: Record<Role, "info" | "warning" | "muted"> = {
    admin: "info",
    editor: "warning",
    viewer: "muted",
  };
  return <Badge variant={v[role]}>{role}</Badge>;
}

function CreateUserModal({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const [err, setErr] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () =>
      api("/api/users", {
        method: "POST",
        body: JSON.stringify({ email, password, role }),
      }),
    onSuccess: onSaved,
    onError: (e: any) => setErr(e?.message || "Falha"),
  });

  return (
    <Modal title="Novo usuário" onClose={onClose}>
      <div className="space-y-3 text-sm">
        <label className="block">
          <div className="text-slate-600 mb-1">Email</div>
          <input
            className="border border-border rounded px-2 py-1 w-full"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoFocus
          />
        </label>
        <label className="block">
          <div className="text-slate-600 mb-1">Senha (mín. 8 caracteres)</div>
          <input
            type="password"
            className="border border-border rounded px-2 py-1 w-full"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <label className="block">
          <div className="text-slate-600 mb-1">Papel</div>
          <select
            className="border border-border rounded px-2 py-1 w-full bg-white"
            value={role}
            onChange={(e) => setRole(e.target.value as Role)}
          >
            <option value="viewer">viewer — leitura</option>
            <option value="editor">editor — CRUD de tools/conexões</option>
            <option value="admin">admin — tudo</option>
          </select>
        </label>
        {err && <div className="text-red-700 text-xs">{err}</div>}
        <div className="flex justify-end gap-2 pt-2">
          <button className="px-3 py-1.5 hover:bg-muted rounded" onClick={onClose}>
            Cancelar
          </button>
          <button
            className="bg-primary text-white px-3 py-1.5 rounded disabled:opacity-50"
            disabled={!email || password.length < 8 || save.isPending}
            onClick={() => {
              setErr(null);
              save.mutate();
            }}
          >
            Criar
          </button>
        </div>
      </div>
    </Modal>
  );
}

function EditUserModal({
  user,
  isSelf,
  onClose,
  onSaved,
}: {
  user: User;
  isSelf: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [email, setEmail] = useState(user.email);
  const [role, setRole] = useState<Role>(user.role);
  const [disabled, setDisabled] = useState(user.disabled);
  const [err, setErr] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () =>
      api(`/api/users/${user.id}`, {
        method: "PUT",
        body: JSON.stringify({ email, role, disabled }),
      }),
    onSuccess: onSaved,
    onError: (e: any) => setErr(e?.message || "Falha"),
  });

  return (
    <Modal title={`Editar ${user.email}`} onClose={onClose}>
      <div className="space-y-3 text-sm">
        <label className="block">
          <div className="text-slate-600 mb-1">Email</div>
          <input
            className="border border-border rounded px-2 py-1 w-full"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
        <label className="block">
          <div className="text-slate-600 mb-1">Papel</div>
          <select
            className="border border-border rounded px-2 py-1 w-full bg-white disabled:bg-muted"
            value={role}
            onChange={(e) => setRole(e.target.value as Role)}
            disabled={isSelf}
            title={isSelf ? "Você não pode alterar o próprio papel" : undefined}
          >
            <option value="viewer">viewer</option>
            <option value="editor">editor</option>
            <option value="admin">admin</option>
          </select>
        </label>
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={disabled}
            onChange={(e) => setDisabled(e.target.checked)}
            disabled={isSelf}
          />
          <span>Desativar conta</span>
          {isSelf && (
            <span className="text-xs text-slate-500">(você não pode se desativar)</span>
          )}
        </label>
        {err && <div className="text-red-700 text-xs">{err}</div>}
        <div className="flex justify-end gap-2 pt-2">
          <button className="px-3 py-1.5 hover:bg-muted rounded" onClick={onClose}>
            Cancelar
          </button>
          <button
            className="bg-primary text-white px-3 py-1.5 rounded disabled:opacity-50"
            disabled={save.isPending}
            onClick={() => {
              setErr(null);
              save.mutate();
            }}
          >
            Salvar
          </button>
        </div>
      </div>
    </Modal>
  );
}

function PasswordModal({
  user,
  onClose,
  onSaved,
}: {
  user: User;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [pw, setPw] = useState("");
  const [confirm, setConfirm] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  const save = useMutation({
    mutationFn: () =>
      api(`/api/users/${user.id}/password`, {
        method: "POST",
        body: JSON.stringify({ password: pw }),
      }),
    onSuccess: () => {
      setDone(true);
      setTimeout(onSaved, 800);
    },
    onError: (e: any) => setErr(e?.message || "Falha"),
  });

  return (
    <Modal title={`Alterar senha — ${user.email}`} onClose={onClose}>
      <div className="space-y-3 text-sm">
        <label className="block">
          <div className="text-slate-600 mb-1">Nova senha (mín. 8 caracteres)</div>
          <input
            type="password"
            className="border border-border rounded px-2 py-1 w-full"
            value={pw}
            onChange={(e) => setPw(e.target.value)}
          />
        </label>
        <label className="block">
          <div className="text-slate-600 mb-1">Confirmar</div>
          <input
            type="password"
            className="border border-border rounded px-2 py-1 w-full"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </label>
        {err && <div className="text-red-700 text-xs">{err}</div>}
        {done && <div className="text-green-700 text-xs">Senha atualizada.</div>}
        <div className="flex justify-end gap-2 pt-2">
          <button className="px-3 py-1.5 hover:bg-muted rounded" onClick={onClose}>
            Cancelar
          </button>
          <button
            className="bg-primary text-white px-3 py-1.5 rounded disabled:opacity-50"
            disabled={pw.length < 8 || pw !== confirm || save.isPending}
            onClick={() => {
              setErr(null);
              save.mutate();
            }}
          >
            Salvar
          </button>
        </div>
      </div>
    </Modal>
  );
}

function Modal({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <div className="fixed inset-0 z-50 bg-black/40 flex items-center justify-center p-4">
      <div className="bg-white rounded shadow-lg w-full max-w-md">
        <div className="flex items-center justify-between border-b border-border px-4 py-2">
          <h2 className="font-semibold text-sm">{title}</h2>
          <button onClick={onClose} className="text-slate-500 hover:text-slate-800">
            ✕
          </button>
        </div>
        <div className="p-4">{children}</div>
      </div>
    </div>
  );
}
