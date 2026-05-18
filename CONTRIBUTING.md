# Contribuindo

Obrigado por considerar contribuir com o ContextForge.

## Ambiente local

Requisitos:

- Go 1.22+
- Node.js 20+
- Docker e Docker Compose
- PostgreSQL e Redis, se nao usar Docker Compose

Suba a infraestrutura local:

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

## Antes de enviar um PR

Execute os comandos relevantes:

```powershell
cd backend
go build ./...
go test ./...
```

```powershell
cd frontend
./node_modules/.bin/tsc --noEmit -p .
npm run build
```

Se alterar comportamento de banco, backup, execucao de tools, autenticacao ou MCP, inclua testes quando viavel.

## Padroes de codigo

- Backend em Go idiomatico, erros explicitos e handlers sem `panic`.
- Frontend com React, TypeScript, Vite, Tailwind e mensagens visiveis em portugues.
- Use helpers existentes antes de criar novas abstracoes.
- Mantenha alteracoes pequenas e focadas.
- Para migrations, sempre inclua `up.sql` e `down.sql`.

Leia tambem [AGENT.md](AGENT.md), que documenta convencoes internas do repositorio.

## Seguranca

Nao inclua segredos em commits. Isso inclui `.env`, `.mcpbak`, tokens MCP, `MASTER_KEY`, `JWT_SECRET`, chaves OpenRouter, senhas e dumps de banco.

Nao relaxe protecoes em `backend/internal/drivers/safety.go` sem explicar claramente o motivo no PR.

## Issues e Pull Requests

Ao abrir uma issue, descreva:

- O que voce esperava que acontecesse.
- O que aconteceu.
- Passos para reproduzir.
- Logs relevantes sem segredos.

Ao abrir um PR, descreva:

- O problema resolvido.
- A abordagem escolhida.
- Testes executados.
- Riscos ou migracoes necessarias.
