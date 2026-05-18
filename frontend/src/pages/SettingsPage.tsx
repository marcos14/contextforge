import { useQuery } from "@tanstack/react-query";
import { api, getAuth } from "../auth";
import { Badge } from "../components/Badge";

type Settings = {
  master_key_fingerprint?: string;
  llm?: { configured: boolean; model?: string; base_url?: string };
  redis?: { configured: boolean; status?: "up" | "down"; error?: string };
  database?: { status?: "up" | "down"; error?: string };
  counts?: Record<string, number>;
};

export function SettingsPage() {
  const auth = getAuth();
  const isAdmin = auth?.role === "admin";

  const q = useQuery({
    queryKey: ["settings"],
    queryFn: () => api<Settings>("/api/settings"),
    enabled: isAdmin,
    refetchInterval: 30_000,
  });

  if (!isAdmin) {
    return (
      <div className="max-w-xl">
        <h1 className="text-2xl font-bold mb-2">Configurações</h1>
        <div className="rounded border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
          Esta seção é restrita a administradores.
        </div>
      </div>
    );
  }

  const s = q.data;

  return (
    <div className="max-w-4xl">
      <div className="mb-4">
        <h1 className="text-2xl font-bold">Configurações</h1>
        <p className="text-sm text-slate-500">
          Informações do servidor (apenas leitura). Para alterar, ajuste as variáveis de
          ambiente e reinicie o serviço.
        </p>
      </div>

      {q.isLoading && <div className="text-sm text-slate-500">Carregando…</div>}
      {q.error && (
        <div className="text-sm text-red-700 border border-red-200 bg-red-50 rounded p-3">
          Erro ao carregar: {(q.error as Error).message}
        </div>
      )}

      {s && (
        <div className="grid gap-4 md:grid-cols-2">
          <Card title="Banco de dados">
            <StatusRow
              label="Status"
              value={
                s.database?.status === "up" ? (
                  <Badge variant="success">online</Badge>
                ) : (
                  <Badge variant="danger">offline</Badge>
                )
              }
            />
            {s.database?.error && (
              <div className="text-xs text-red-700 mt-1">{s.database.error}</div>
            )}
          </Card>

          <Card title="Redis">
            <StatusRow
              label="Configurado"
              value={s.redis?.configured ? "Sim" : "Não"}
            />
            {s.redis?.configured && (
              <StatusRow
                label="Status"
                value={
                  s.redis?.status === "up" ? (
                    <Badge variant="success">online</Badge>
                  ) : (
                    <Badge variant="danger">offline</Badge>
                  )
                }
              />
            )}
            {s.redis?.error && (
              <div className="text-xs text-red-700 mt-1">{s.redis.error}</div>
            )}
          </Card>

          <Card title="LLM (OpenRouter)">
            <StatusRow
              label="Configurado"
              value={s.llm?.configured ? "Sim" : "Não"}
            />
            {s.llm?.configured && (
              <>
                <StatusRow label="Modelo" value={<code className="text-xs">{s.llm.model}</code>} />
                <StatusRow
                  label="Base URL"
                  value={<code className="text-xs break-all">{s.llm.base_url}</code>}
                />
              </>
            )}
          </Card>

          <Card title="Criptografia">
            <StatusRow
              label="MASTER_KEY fingerprint"
              value={
                s.master_key_fingerprint ? (
                  <code className="text-xs">{s.master_key_fingerprint}</code>
                ) : (
                  "—"
                )
              }
            />
            <div className="text-xs text-slate-500 mt-2">
              Backups só são restauráveis em instâncias com o mesmo fingerprint.
            </div>
          </Card>

          {s.counts && (
            <Card title="Visão geral" wide>
              <div className="grid grid-cols-2 sm:grid-cols-5 gap-3 text-center">
                {Object.entries(s.counts).map(([k, v]) => (
                  <div key={k} className="border border-border rounded p-3 bg-muted/40">
                    <div className="text-2xl font-bold">{v}</div>
                    <div className="text-xs text-slate-500 capitalize">{k}</div>
                  </div>
                ))}
              </div>
            </Card>
          )}
        </div>
      )}
    </div>
  );
}

function Card({
  title,
  children,
  wide,
}: {
  title: string;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <div className={"border border-border rounded bg-white p-4 " + (wide ? "md:col-span-2" : "")}>
      <h2 className="font-semibold text-sm mb-2">{title}</h2>
      <div className="space-y-1.5 text-sm">{children}</div>
    </div>
  );
}

function StatusRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-slate-500 text-sm">{label}</span>
      <span>{value}</span>
    </div>
  );
}
