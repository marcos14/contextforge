# AGENT.md

Guia operacional para agentes de IA (Claude, Copilot, Cursor, etc.) que vão ler,
editar ou estender este repositório. Leia tudo antes de propor mudanças.

> **Nota:** este arquivo é a fonte da verdade. `CLAUDE.md` aponta para o
> mesmo conteúdo via link de arquivo (hard link no Windows / symlink em
> POSIX). Edite **um** dos dois — o outro reflete automaticamente.

---

## 1. O que é o projeto

**ContextForge** é um servidor administrativo + UI que expõe fontes de dados
(SQL, REST, Mongo, etc.) como ferramentas MCP (Model Context Protocol) que
clientes de IA podem invocar via tokens. Há um backend Go e uma SPA React.

Componentes principais:

- **Backend Go** (`backend/`): API REST de administração, servidor MCP
  (`/mcp`), execução de tools (SQL/Mongo/REST/JS), cripto, JWT, cache Redis.
- **Frontend React** (`frontend/`): SPA Vite + TypeScript + Tailwind +
  TanStack Query + React Router v6.
- **Postgres**: estado persistente (conexões, grupos, tools, tokens, audit).
- **Redis**: cache de execução e rate limit.
- **Docker Compose** (`deploy/`): orquestração local.

---

## 2. Stack e versões

| Camada | Tecnologia |
| --- | --- |
| Backend | Go 1.22+, chi v5, pgx/v5, golang-migrate, JWT HS256, bcrypt, AES-256-GCM, HKDF, goja (JS sandbox para code tools) |
| Frontend | React 18, TypeScript, Vite, Tailwind, @tanstack/react-query, React Router v6 |
| DB | PostgreSQL (migrations em `backend/internal/store/migrations`) |
| Cache | Redis |
| Build/Run | Docker, docker-compose |

---

## 3. Layout do repositório

```
backend/
  cmd/contextforge/main.go        # entrypoint
  internal/
    api/                        # HTTP admin (handlers_*.go) + Mount() em handlers_llm_router.go
    auth/                       # JWT, bcrypt, hashing de tokens
    backup/                     # Export/import criptografado (.mcpbak)
    cache/                      # wrapper Redis
    codetool/                   # runtime JS (goja) para tools tipo "code"
    config/                     # carregamento de env vars (MASTER_KEY, etc.)
    crypto/aesgcm.go            # Cipher AES-256-GCM com DEK por registro
    drivers/                    # pg, mysql, mssql, oracle, mongo, firebird, rest + safety.go
    executor/                   # orquestra execução de tools (cache + driver)
    llm/                        # cliente OpenRouter + prompts
    mcpsrv/                     # servidor MCP exposto em /mcp
    ratelimit/                  # rate limiting por token
    registry/                   # snapshot in-memory da config
    store/                      # pool pgx, migrações, models
frontend/
  src/
    App.tsx                     # sidebar/menu + layout
    auth.tsx                    # api(), apiBlob(), RequireAuth
    main.tsx                    # rotas
    pages/                      # uma página por seção do menu
    components/                 # Badge, EmptyState, Pagination, CodeEditor
    utils/format.ts
deploy/                         # Dockerfiles + docker-compose.yml
```

---

## 4. Padrões de código

### Backend
- **Rotas**: registradas em `internal/api/handlers_llm_router.go::Mount`.
  Grupos: público, autenticado, `admin+editor`, `admin` apenas.
- **Middleware de auth**: `a.RequireAuth("admin","editor")`. Use sempre o
  conjunto mínimo de roles necessário.
- **Helpers**: `writeJSON`, `writeErr`, `decodeBody`, `parseListParams`,
  `writePage`. Use-os em vez de duplicar lógica.
- **Listagens** suportam dois modos por compatibilidade:
  - **Legado**: sem query params → retorna array JSON puro.
  - **Paginado**: `?page=&page_size=&q=` → retorna `{items,total,page,page_size}`.
- **Erro JSON**: sempre `{"error": "mensagem"}`.
- **Contexto do usuário**: `currentUser(ctx)` retorna `(uuid, role, ok)`.
- **SQL**: use `a.Pool.Exec/Query/QueryRow` com placeholders `$1, $2...`. Para
  operações multi-step que precisam ser atômicas, abra `tx := a.Pool.BeginTx`.
- **JSON nullable em colunas JSONB**: use `nullableJSON(raw)` (em
  `handlers_tools.go` e `backup.go`) — converte `nil`/vazio para `NULL`.
- **Auditoria**: insira em `audit_logs` para ações sensíveis. Veja
  `handlers_backup.go::audit`.
- **Idiomático Go**: erros explícitos, sem panic em handlers, funções curtas.

### Frontend
- **Mensagens visíveis em português**, código/comentários em inglês.
- **HTTP**: use `api<T>(path, init)` para JSON e `apiBlob(path, init)` para
  download binário (ambos em `auth.tsx`). Header `Authorization` é injetado
  automaticamente; 401 redireciona para `/login`.
- **Multipart upload**: monte `FormData`; `api()` detecta e remove
  `Content-Type` automaticamente.
- **Queries**: TanStack Query com `queryKey` por recurso. Após mutação,
  `qc.invalidateQueries`.
- **Estilo**: Tailwind utilities. Reuse `Badge`, `EmptyState`, `Pagination`,
  `CodeEditor`. Cores: `bg-primary`, `bg-muted`, `border-border`.
- **Rotas**: declare em `main.tsx`. Item de menu em `App.tsx` (`links` array).
- **Rules of Hooks**: nada de `useState`/`useQuery` depois de `if (...) return`.

---

## 5. Segurança e cuidados

1. **`MASTER_KEY`** é obrigatório (32 bytes random, base64). Sem ela o
   servidor não sobe.
2. **Cripto sensível** sempre via `crypto.Cipher.Encrypt(plain, aad)`. O AAD
   amarra o blob ao registro (ex.: `connection:<uuid>`). Se for mover/duplicar
   uma conexão, **re-encripte com o novo AAD** (ver `backup.applyConnection`
   action `duplicate`).
3. **Senhas**: bcrypt (`auth.HashPassword`, cost 12).
4. **Token MCP**: armazenado apenas como SHA-256; o segredo plaintext só é
   exibido na criação. **Nunca** o exporte/logue.
5. **Backups (`.mcpbak`)**: criptografados com a `MASTER_KEY` do servidor.
   **Só restauram em uma instância com a mesma `MASTER_KEY`**. Tokens,
   usuários e audit logs estão **fora do escopo** do backup.
6. **Drivers**: cuidados de segurança vivem em `drivers/safety.go` (parser de
   SQL, limites, etc.). **Não remova/relaxe** essas verificações sem motivo
   forte e documentado.
7. **Tools tipo "code"** rodam JS em `codetool/runtime.go` (goja). O sandbox
   tem timeout e limites; preserve-os.
8. **OWASP**: cuidado especial com SQL injection nos drivers. Parametrize
   tudo. Não interpole strings em queries.
9. **Logs**: evite logar segredos, query texts com PII ou tokens.

---

## 6. Banco de dados

- Migrations: `backend/internal/store/migrations/NNNN_xxx.{up,down}.sql`.
  Sempre **incluir o `down.sql`**.
- Aplicação roda `Migrate(ctx, url)` em `main.go`.
- Tabelas-chave: `users`, `connections`, `tool_groups`, `tools`,
  `tool_versions`, `tokens`, `token_grants`, `group_visibility_overrides`,
  `tool_executions`, `audit_logs`.
- Constraints UNIQUE importantes: `connections.name`, `tool_groups.name`,
  `tools.slug`, `tokens.prefix`.
- `tools.slug` precisa casar `^[a-z0-9_]{1,64}$`.
- Toda atualização de tool bumpa `tools.version` e grava em `tool_versions`.

---

## 7. Sempre executar antes de finalizar

Antes de declarar uma tarefa concluída, agentes **devem**:

1. **Backend compila**:
   ```
   go build ./...
   ```
   Sem Go local? Usar Docker:
   ```
   docker run --rm -v "PATH_TO_PROJECT\backend:/app" -w /app golang:latest go build ./...
   ```
2. **Testes unitários Go**:
   ```
   go test ./...
   ```
   (Docker:
   `docker run --rm -v "PATH_TO_PROJECT\backend:/app" -w /app golang:latest go test ./...`).
3. **Frontend type-check**:
   ```
   cd frontend && ./node_modules/.bin/tsc --noEmit -p .
   ```
4. **Build do frontend** (quando mudou UI):
   ```
   npm run build
   ```

Se a mudança impactar features de fronteira (multi-arquivo, DB, integração
entre serviços), **adicione/atualize testes** em:

- `backend/internal/<pkg>/<pkg>_test.go` (use o padrão de
  `internal/backup/backup_test.go` para round-trip puro; para DB use
  testcontainers ou mocks — ainda não há infra, criar se necessário).
- E2E: ainda **não há** uma suíte E2E. Para mudanças que afetam o fluxo
  completo (admin UI → API → DB → MCP), prefira adicionar um teste de
  integração em Go com Postgres via testcontainers, ou um script Playwright
  em `frontend/e2e/` (criar se for o primeiro). **Documente** o comando de
  execução no PR.

> **Regra de ouro**: nunca finalize uma tarefa com `go build` ou `tsc`
> falhando. Nunca remova testes existentes para "fazer passar".

---

## 8. Convenções de commits e PRs

- Mensagens curtas no imperativo; assunto em português ou inglês,
  consistente com o histórico do branch.
- Não suba o `.mcpbak` em commits — é dado.
- Não suba `MASTER_KEY` real, secrets de OpenRouter, etc. Use `.env` (não
  commitado).
- Quando criar migrations, gere também o `down.sql` reverso e teste rollback.

---

## 9. Coisas que **não** devem ser feitas

- Não relaxar `drivers/safety.go` sem aprovação explícita.
- Não introduzir SQL com concatenação de strings.
- Não exportar segredos plaintext (tokens MCP, senhas, configs de conexão)
  em rotas/list endpoints.
- Não criar arquivos Markdown documentando mudanças (a menos que o usuário
  peça). Atualize código + AGENT.md quando necessário.
- Não rodar `git push --force`, `git reset --hard`, ou DROP de tabela sem
  pedir confirmação ao usuário.
- Não adicionar dependências pesadas (CSS frameworks, ORMs, etc.) sem
  justificativa. Stack atual é deliberadamente enxuta.

---

## 10. Como rodar localmente

```
# Backend + DB + Redis via docker-compose
cd deploy && docker compose up -d

# Frontend dev
cd frontend && npm install && npm run dev
```

Variáveis essenciais (`backend/internal/config/config.go`):

- `DATABASE_URL` — postgres://...
- `REDIS_URL`
- `MASTER_KEY` — 32 bytes base64
- `JWT_SECRET` — HS256 secret
- `BOOTSTRAP_ADMIN_EMAIL` / `BOOTSTRAP_ADMIN_PASSWORD` (primeiro boot)
- `OPENROUTER_API_KEY` (opcional, para assistentes LLM)

---

## 11. Onde olhar primeiro ao editar

| Tarefa | Arquivos |
| --- | --- |
| Adicionar uma rota | `internal/api/handlers_<area>.go` + `Mount()` em `handlers_llm_router.go` |
| Novo driver de fonte de dados | `internal/drivers/<name>/driver.go` + registrar em `drivers/registry.go` |
| Mudar schema DB | nova migration `store/migrations/NNNN_*.{up,down}.sql` |
| Cripto | `internal/crypto/aesgcm.go` (AAD é obrigatório) |
| Nova página UI | `frontend/src/pages/<Name>Page.tsx` + entry em `App.tsx::links` + rota em `main.tsx` |
| Wrapper HTTP no front | `frontend/src/auth.tsx` (`api`, `apiBlob`) |
| Backup/restore | `internal/backup/backup.go` + `internal/api/handlers_backup.go` + `frontend/src/pages/BackupPage.tsx` |

---

## 12. Roles e permissões

| Role | Pode |
| --- | --- |
| `admin` | tudo, incluindo gestão de usuários e backup |
| `editor` | CRUD de conexões, grupos, tools, tokens, grants, backup |
| `viewer` | leitura: `/me`, `/registry`, `/executions` |

Use sempre o conjunto **mínimo** de roles em `RequireAuth(...)`.

---

Última revisão: backup/restore criptografado (rotina `/api/backup/*` +
página `BackupPage`).
