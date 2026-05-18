# ContextForge

ContextForge e uma plataforma self-hosted para transformar fontes de dados em tools MCP (Model Context Protocol). Ele oferece uma UI administrativa, um backend Go e um endpoint MCP HTTP Streamable unico para clientes de IA consumirem dados com tokens, grants, rate limit, auditoria e cache.

> Status: projeto em desenvolvimento ativo. A API e o schema ainda podem mudar entre releases.

## Principais recursos

- Endpoint MCP HTTP Streamable em `/mcp`.
- UI administrativa React para gerenciar conexoes, grupos, tools, tokens, usuarios, auditoria e backup.
- Backend Go com API REST, JWT, Redis, Postgres e migrations automaticas.
- Suporte a PostgreSQL, MySQL/MariaDB, SQL Server, Oracle, MongoDB, Firebird 5 e REST GET.
- Tools baseadas em SQL, Mongo, REST e codigo JavaScript sandboxed.
- Geracao assistida por LLM via OpenRouter, quando `OPENROUTER_API_KEY` estiver configurada.
- Credenciais criptografadas com AES-256-GCM e DEK derivada por registro via HKDF.
- Tokens MCP armazenados apenas como hash SHA-256; o segredo em texto claro aparece somente na criacao.
- Backup/restore criptografado para conexoes, grupos e tools.

## Stack

| Camada | Tecnologia |
| --- | --- |
| Backend | Go, chi, pgx, golang-migrate, JWT, bcrypt, AES-256-GCM, goja |
| Frontend | React 18, TypeScript, Vite, Tailwind, TanStack Query, React Router |
| Banco | PostgreSQL |
| Cache/rate limit | Redis |
| Deploy local | Docker Compose |

## Requisitos

- Go 1.22+
- Node.js 20+
- Docker e Docker Compose
- PostgreSQL e Redis, caso rode sem Docker

## Inicio rapido com Docker

Crie o arquivo `.env` a partir do exemplo:

```powershell
Copy-Item .env.example .env
```

Gere valores seguros para `MASTER_KEY` e `JWT_SECRET`:

```powershell
$bytes = New-Object byte[] 32
[System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$masterKey = [Convert]::ToBase64String($bytes)

$jwtBytes = New-Object byte[] 48
[System.Security.Cryptography.RandomNumberGenerator]::Fill($jwtBytes)
$jwtSecret = [Convert]::ToBase64String($jwtBytes)

(Get-Content .env) `
  -replace 'CHANGE_ME_BASE64_32BYTES', $masterKey `
  -replace 'CHANGE_ME_LONG_RANDOM_STRING', $jwtSecret `
  | Set-Content .env
```

Edite tambem no `.env`:

- `BOOTSTRAP_ADMIN_EMAIL`
- `BOOTSTRAP_ADMIN_PASSWORD`
- `OPENROUTER_API_KEY`, opcional

Suba a stack:

```powershell
cd deploy
docker compose up --build
```

A UI ficara em <http://localhost:8080>.

O endpoint MCP ficara em <http://localhost:8080/mcp>. Use o header `Authorization: Bearer <token>` com um token criado na tela **Tokens**.

## Desenvolvimento local

Infra local:

```powershell
cd deploy
docker compose up -d postgres redis
```

Backend:

```powershell
cd backend
go mod tidy
go run ./cmd/contextforge
```

Frontend:

```powershell
cd frontend
npm install
npm run dev
```

O Vite roda em <http://localhost:5173> e faz proxy de `/api` e `/mcp` para o backend.

## Variaveis de ambiente

| Variavel | Obrigatoria | Descricao |
| --- | --- | --- |
| `DATABASE_URL` | Sim | URL do Postgres de metadados. |
| `REDIS_URL` | Sim | URL do Redis para cache e rate limit. |
| `MASTER_KEY` | Sim | Chave base64 de 32 bytes para criptografia. Guarde com cuidado. |
| `JWT_SECRET` | Sim | Segredo para assinar sessoes da UI. Use 32+ caracteres. |
| `BOOTSTRAP_ADMIN_EMAIL` | Nao | E-mail do admin inicial quando nao ha usuarios. |
| `BOOTSTRAP_ADMIN_PASSWORD` | Sim no primeiro boot | Senha do admin inicial quando nao ha usuarios. |
| `OPENROUTER_API_KEY` | Nao | Habilita geracao assistida por LLM. |
| `OPENROUTER_MODEL` | Nao | Modelo OpenRouter usado pelo assistente. |
| `PUBLIC_BASE_URL` | Nao | URL publica usada em instrucoes e clientes. |
| `CORS_ALLOWED_ORIGINS` | Nao | Origens permitidas separadas por virgula. |
| `RATE_LIMIT_IP_PER_MIN` | Nao | Limite global por IP por minuto. |
| `METRICS_ENABLED` | Nao | Habilita `/metrics`. |

Veja [.env.example](.env.example) para a lista completa.

## Layout do repositorio

```text
backend/
  cmd/contextforge/        # entrypoint unificado
  internal/api/          # API REST administrativa
  internal/auth/         # JWT, bcrypt e hashing de tokens MCP
  internal/backup/       # export/import criptografado (.mcpbak)
  internal/cache/        # Redis cache helper
  internal/codetool/     # runtime JS sandboxed para tools tipo code
  internal/config/       # carregamento de variaveis de ambiente
  internal/crypto/       # AES-256-GCM e HKDF
  internal/drivers/      # drivers pg, mysql, mssql, oracle, mongo, firebird, rest
  internal/executor/     # execucao de tools, cache e rate limit
  internal/llm/          # cliente OpenRouter e prompts
  internal/mcpsrv/       # servidor MCP
  internal/registry/     # snapshot em memoria de tools e tokens
  internal/store/        # pgx, models e migrations
frontend/
  src/pages/             # paginas da UI administrativa
  src/components/        # componentes compartilhados
deploy/
  docker-compose.yml
  backend.Dockerfile
  backend.oracle.Dockerfile
```

## Modelo de seguranca

- `MASTER_KEY` e obrigatoria e deve ser 32 bytes base64. Sem ela o servidor nao inicia.
- Credenciais de conexao sao criptografadas no Postgres com AES-256-GCM.
- Cada registro usa AAD proprio, como `connection:<uuid>`, para evitar troca indevida de blobs criptografados.
- Tokens MCP usam formato `mcb_<prefix>_<secret>` e o secret e salvo apenas como SHA-256.
- Queries SQL passam por verificacoes em `backend/internal/drivers/safety.go`; por padrao sao permitidas apenas consultas (`SELECT`, `WITH`, `SHOW`, `EXPLAIN`).
- Parametros de queries devem ser nomeados, como `:cliente_id`, e sao convertidos para placeholders posicionais do dialeto.
- Use usuarios read-only nas conexoes de banco sempre que possivel.
- Tools tipo `code` rodam em sandbox JavaScript com timeout e limites.
- Backups `.mcpbak` sao criptografados com a `MASTER_KEY` do servidor.
- Nunca publique `.env`, `.mcpbak`, tokens MCP, senhas ou chaves de provedores LLM.

## Backup e restauracao

A tela **Backup** permite exportar e importar conexoes, grupos e tools, incluindo historico de versoes. O arquivo `.mcpbak` e criptografado com AES-256-GCM usando a `MASTER_KEY` do servidor.

Importante: um backup so pode ser restaurado em uma instancia com a mesma `MASTER_KEY`. Tokens MCP, usuarios e audit logs ficam fora do backup.

Na restauracao, a UI mostra uma pre-visualizacao com diff e permite pular, sobrescrever ou duplicar itens.

## Oracle

O driver Oracle depende do Oracle Instant Client por causa do `godror`. Use o profile opcional:

```powershell
cd deploy
docker compose --profile oracle up contextforge-oracle
```

Para uma imagem real com Oracle, habilite build tags e ajuste `deploy/backend.oracle.Dockerfile` conforme as instrucoes do proprio arquivo.

## Validacao

Backend:

```powershell
cd backend
go build ./...
go test ./...
```

Frontend:

```powershell
cd frontend
./node_modules/.bin/tsc --noEmit -p .
npm run build
```

## Publicacao segura

Antes de publicar o repositorio:

```powershell
git ls-files | Select-String -Pattern '^\.env$|\.mcpbak$|secret|token|key'
git log --all --full-history -- .env
```

O `.env` real deve continuar fora do git. Se uma chave ja apareceu em terminal, log, issue, chat ou commit, considere vazada e rotacione.

## Contribuindo

Leia [CONTRIBUTING.md](CONTRIBUTING.md) para configurar o ambiente e abrir PRs.

Para reportar vulnerabilidades, leia [SECURITY.md](SECURITY.md).

Agentes de IA devem ler [AGENT.md](AGENT.md) antes de propor mudancas.

## Licenca

Este projeto e distribuido sob a GNU Affero General Public License v3.0. Veja [LICENSE](LICENSE).
