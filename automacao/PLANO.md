# PLANO — Módulo "Query Studio" (Assistente de Consultas SQL)

> Documento de planejamento. Não é código. Descreve um **novo módulo/menu**
> do ContextForge focado em **entender qual consulta o usuário precisa** e
> **gerar consultas SQL performáticas** para uso em qualquer sistema — não
> necessariamente como uma tool MCP.

---

## 1. Motivação

Hoje o ContextForge já é muito bom em ajudar a criar **tools MCP** a partir de
consultas SQL, e o motor de exploração de schema (`drivers.Introspect`) é
eficiente. Porém, alguns usuários passaram a usar o produto apenas para
**gerar SQL para outros sistemas** (relatórios, ETL, dashboards, código de
aplicação).

O fluxo atual (`ToolsPage` + `/api/llm/chat-tool`) foi desenhado para produzir
uma **tool** (slug, título, `params_schema`, `output_schema`, versão, ativação).
Isso adiciona atrito e conceitos irrelevantes para quem só quer **uma boa
query**. Além disso, o foco atual é *correção funcional* da query — não
*performance* (planos de execução, índices, anti-padrões).

**Objetivo**: criar um módulo dedicado, com menu próprio, cuja única missão é:

1. Conversar com o usuário para **entender a necessidade** da consulta.
2. Gerar SQL **performático e idiomático** para o dialeto da conexão.
3. **Validar/medir** a query (EXPLAIN / EXPLAIN ANALYZE, amostra de dados).
4. **Refinar** iterativamente com base no plano de execução real.
5. Entregar a query pronta para **copiar e usar em outro sistema** (com
   explicação, índices sugeridos e observações de performance).

---

## 2. Como se diferencia do módulo de Tools

| Aspecto | Módulo **Tools** (atual) | Módulo **Query Studio** (novo) |
| --- | --- | --- |
| Objetivo final | Criar/ativar uma tool MCP | Entregar SQL para uso externo |
| Artefato | `tools` (slug, schemas, versão) | Query + explicação + índices sugeridos |
| Parâmetros | `params_schema` JSON Schema (obrigatório p/ MCP) | Parâmetros opcionais/livres, foco em legibilidade |
| Foco do LLM | Correção + contrato MCP | **Performance** + correção + didática |
| Validação | Dry-run + ativação | EXPLAIN/ANALYZE + amostra + heurísticas |
| Persistência | Tabela `tools`, versionada | Histórico leve de "sessões de query" (opcional) |
| Roles | admin + editor | admin + editor (mesma fronteira) |

O novo módulo **reaproveita ao máximo** a infraestrutura existente: conexões,
`Introspect`, cliente OpenRouter (`llm.Client`), padrões de rota (`Mount`) e
padrões de página React (`api()` + TanStack Query).

---

## 3. Nome e navegação

- **Nome do menu**: `Query Studio` (rótulo visível em PT pode ser
  "Estúdio de Consultas").
- **Rota**: `/query-studio`.
- **Ícone**: reutilizar estilo dos ícones existentes em `App.tsx` (ex.: ícone
  de "banco/consulta").
- **Posição no menu**: logo abaixo de **Tools**, pois compartilham o conceito
  de conexão + schema.

---

## 4. Experiência do usuário (fluxo)

```mermaid
flowchart TD
    A[Seleciona Conexão] --> B[Explora Schema<br/>tabelas, colunas, FKs, índices]
    B --> C[Descreve a necessidade<br/>em linguagem natural]
    C --> D[LLM propõe SQL performático<br/>+ explicação + índices sugeridos]
    D --> E{Validar?}
    E -->|EXPLAIN| F[Plano de execução estimado]
    E -->|EXPLAIN ANALYZE<br/>opcional/seguro| G[Plano real + tempos]
    E -->|Amostra| H[Preview de N linhas]
    F --> I[LLM refina usando o plano]
    G --> I
    H --> I
    I --> D
    D --> J[Copiar SQL / Exportar<br/>ou 'Promover a Tool']
```

Passos detalhados:

1. **Escolher conexão**: dropdown alimentado por `GET /api/connections`.
2. **Explorar schema**: chamar `GET /api/connections/{id}/introspect` (motor
   atual). Ver seção 7 sobre **enriquecer** a introspecção com FKs e índices.
3. **Descrever a necessidade**: chat livre. O usuário pode falar objetivos de
   negócio ("faturamento por cliente no último trimestre"), não SQL.
4. **Receber proposta**: o LLM retorna:
   - `query` (SQL para o dialeto),
   - `explanation` (o que a query faz, em PT),
   - `suggested_indexes` (DDL de índices que acelerariam a query),
   - `performance_notes` (anti-padrões evitados, avisos),
   - `assumptions` (premissas que fez sobre o schema).
5. **Validar** (botões):
   - **EXPLAIN** (padrão, seguro, sem executar a query).
   - **EXPLAIN ANALYZE** (opcional; executa de fato — ver seção 8 Segurança).
   - **Preview** (amostra limitada de linhas, com `LIMIT` forçado).
6. **Refinar**: o resultado do EXPLAIN/erro/amostra volta ao LLM para nova
   iteração ("o índice X não é usado, reescreva evitando função na coluna Y").
7. **Entregar**:
   - **Copiar SQL** (com/sem comentários).
   - **Exportar** (`.sql`).
   - **Promover a Tool** (atalho que abre o fluxo de criação de tool já
     preenchido — ponte entre os dois módulos, opcional na Fase 3).

---

## 5. Arquitetura — Backend

### 5.1 Novos endpoints (grupo `admin` + `editor`, registrados em `Mount`)

Registrar em `internal/api/handlers_llm_router.go::Mount`, no grupo
`RequireAuth("admin","editor")`, junto aos demais `/llm/*`:

| Método | Rota | Handler | Descrição |
| --- | --- | --- | --- |
| POST | `/api/query-studio/chat` | `QueryStudioChat` | Multi-turn: recebe schema + histórico + query atual; devolve SQL + explicação + índices + notas de performance. |
| POST | `/api/query-studio/explain` | `QueryStudioExplain` | Roda `EXPLAIN` (e opcionalmente `ANALYZE`) da query proposta e devolve o plano estruturado/textual. |
| POST | `/api/query-studio/preview` | `QueryStudioPreview` | Executa a query com `LIMIT` forçado e devolve colunas + amostra (reusa `driver.Query`/executor com limites). |
| GET | `/api/query-studio/sessions` | `ListQuerySessions` | (Opcional, Fase 2) lista sessões salvas do usuário. Suporta paginação (`page`, `page_size`, `q`). |
| POST | `/api/query-studio/sessions` | `SaveQuerySession` | (Opcional, Fase 2) salva uma sessão (título + query + chat). |
| GET | `/api/query-studio/sessions/{id}` | `GetQuerySession` | (Opcional) detalhe de uma sessão. |
| DELETE | `/api/query-studio/sessions/{id}` | `DeleteQuerySession` | (Opcional) remove sessão. |

Arquivo novo sugerido: `internal/api/handlers_query_studio.go`. Reusar helpers
existentes: `writeJSON`, `writeErr`, `decodeBody`, `parseListParams`,
`writePage`, `currentUser`, `openDriver`.

### 5.2 Novos prompts LLM

Adicionar em `internal/llm/prompts.go` (seguindo o padrão de `ChatTool`):

```go
type QueryStudioInput struct {
    ConnectionKind string        // "pg" | "mysql" | ...
    Tables         []drivers.Table
    Relations      []Relation    // FKs (novo — ver 7)
    Indexes        []IndexInfo   // índices existentes (novo — ver 7)
    History        []Message
    CurrentQuery   string        // query em edição, se houver
    ExplainResult  string        // plano de execução da última validação (feedback loop)
}

type QueryStudioOutput struct {
    Reply            string      // resposta conversacional em PT
    Query            string      // SQL final proposto
    Explanation      string      // o que a query faz
    SuggestedIndexes []string    // DDL de índices sugeridos
    PerformanceNotes []string    // avisos/anti-padrões
    Assumptions      []string    // premissas sobre o schema
    Raw              string      // resposta bruta (debug)
}
```

**System prompt** — pontos-chave (dialeto-aware, como já é feito para
Firebird):

- Papel: "engenheiro de banco de dados sênior especialista em performance".
- **Objetivo é query para uso geral**, não uma tool MCP → **não** exigir
  `params_schema`/`slug`/`output_schema`.
- **Regras de performance** a aplicar e explicar:
  - Evitar `SELECT *`; listar apenas colunas necessárias.
  - Preferir JOINs a subconsultas correlacionadas quando possível.
  - Evitar funções sobre colunas indexadas em `WHERE` (quebra uso de índice).
  - Usar `EXISTS` em vez de `IN (subquery)` grande quando fizer sentido.
  - Alertar sobre `WHERE` ausente / varreduras completas.
  - Paginação com keyset em vez de `OFFSET` alto quando aplicável.
  - Sugerir índices (incluindo compostos e cobertura) coerentes com o dialeto.
  - Considerar tipos/cardinalidade a partir da introspecção.
- **Guias por dialeto** (Postgres/MySQL/MSSQL/Oracle/Firebird): sintaxe de
  `EXPLAIN`, `LIMIT`/`TOP`/`ROWNUM`/`FETCH FIRST`, criação de índice.
- **Somente SELECT** por padrão (sem DDL/DML executável); os índices sugeridos
  são **texto** para o usuário aplicar, nunca executados pelo módulo.
- Saída em **JSON** (`response_format: json_object`), como os prompts atuais.
- Quando receber `ExplainResult`, **usar o plano** para reescrever/otimizar e
  explicar o que mudou.

Método público análogo aos existentes: `func (c *Client) QueryStudioChat(ctx,
QueryStudioInput) (QueryStudioOutput, error)`.

### 5.3 EXPLAIN / análise de performance (por dialeto)

Estender a interface de driver (`internal/drivers/driver.go`) com um método
**opcional** de explain, mantendo compatibilidade (drivers que não suportam
retornam `ErrUnsupported`):

```go
// Optional capability. Drivers without support return drivers.ErrUnsupported.
type Explainer interface {
    Explain(ctx context.Context, query string, analyze bool) (ExplainResult, error)
}

type ExplainResult struct {
    Dialect string
    Plan    string   // texto do plano (ou JSON serializado)
    Format  string   // "text" | "json"
}
```

Sintaxe por dialeto (implementar por driver):

| Dialeto | EXPLAIN | EXPLAIN ANALYZE |
| --- | --- | --- |
| Postgres (`pg`) | `EXPLAIN (FORMAT JSON) <q>` | `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) <q>` |
| MySQL | `EXPLAIN FORMAT=JSON <q>` | `EXPLAIN ANALYZE <q>` (8.0.18+) |
| MSSQL | `SET SHOWPLAN_XML ON` / estimado | Plano de execução real |
| Oracle | `EXPLAIN PLAN FOR <q>` + `DBMS_XPLAN` | via `V$SQL_PLAN` |
| Firebird | `SET PLAN ON` / plano textual | n/d |
| Mongo/REST | **não aplicável** → `ErrUnsupported` (UI desabilita botão) |

**Reuso**: a execução do EXPLAIN e do preview deve passar pelas mesmas
proteções de `drivers/safety.go` (parser SELECT-only, limites) e pelos limites
de tempo/linhas do executor.

### 5.4 Preview de dados

- Reusar a lógica de execução existente (executor/driver `Query`) com
  `LIMIT`/`TOP` **forçado** pelo backend (ex.: 100 linhas), timeout curto e
  sem cache por token (é sandbox de desenvolvimento).
- Nunca confiar no `LIMIT` que o LLM colocou; **impor** limite no servidor.

---

## 6. Arquitetura — Frontend

### 6.1 Wiring da página

- **Nova página**: `frontend/src/pages/QueryStudioPage.tsx`.
- **Menu**: adicionar entry no array `links` de `frontend/src/App.tsx`
  (label "Query Studio", ícone, rota `/query-studio`, roles admin+editor).
- **Rota**: declarar em `frontend/src/main.tsx` sob `RequireAuth`.
- **HTTP**: usar `api<T>()` de `frontend/src/auth.tsx` (auth automática).
- **Estado/dados**: TanStack Query, seguindo `ToolsPage.tsx`:
  - `["conns"]` → `/api/connections`
  - introspecção sob demanda → `/api/connections/{id}/introspect`
  - mutations → `/api/query-studio/chat`, `/explain`, `/preview`.

### 6.2 Layout proposto (3 colunas)

```
┌────────────┬───────────────────────────┬────────────────────┐
│ Schema     │ Chat / Requisito           │ SQL + Performance  │
│ (árvore)   │ (histórico + input)        │                    │
│ tabelas ▸  │ usuário: "faturamento..."  │ [ SQL editor ]     │
│  colunas   │ IA: proposta + explicação  │ Explicação (PT)    │
│  FKs       │                            │ Índices sugeridos  │
│  índices   │                            │ Notas de perf.     │
│            │                            │ [EXPLAIN][Preview] │
│            │                            │ [Copiar][Exportar] │
└────────────┴───────────────────────────┴────────────────────┘
```

- **Coluna Schema**: árvore navegável (reutiliza dados do `Introspect`;
  mostra FKs e índices quando disponíveis). Clicar em tabela/coluna insere
  referência no chat.
- **Coluna Chat**: conversa em linguagem natural; renderiza `reply`,
  `assumptions` e um botão "Usar esta query".
- **Coluna SQL**: `CodeEditor` (componente existente) com a query; abas/painéis
  para **Explicação**, **Índices sugeridos**, **Notas de performance**, e o
  **plano de execução** (resultado do EXPLAIN) + **preview** (tabela de
  amostra). Botões: EXPLAIN, EXPLAIN ANALYZE (com confirmação), Preview,
  Copiar, Exportar `.sql`, e (Fase 3) "Promover a Tool".

### 6.3 Componentes reutilizados

`CodeEditor`, `Badge`, `EmptyState`, `Pagination` (para histórico de sessões).

---

## 7. Enriquecer o motor de exploração de schema (FKs + índices)

O `Introspect` atual retorna apenas `Schema/Name/Columns`. Para **performance**
é muito útil conhecer **relacionamentos (FKs)** e **índices existentes**.

Proposta: adicionar um método **opcional** de introspecção estendida, sem
quebrar o `Introspect` atual:

```go
type RichIntrospector interface {
    IntrospectRich(ctx context.Context) (SchemaGraph, error)
}

type SchemaGraph struct {
    Tables    []Table
    Relations []Relation   // FKs: from(table,col) -> to(table,col)
    Indexes   []IndexInfo  // nome, tabela, colunas, unique
}
```

- **Postgres**: `pg_constraint` (FKs) + `pg_index`/`pg_indexes`.
- **MySQL**: `INFORMATION_SCHEMA.KEY_COLUMN_USAGE` + `STATISTICS`.
- **MSSQL/Oracle/Firebird**: catálogos equivalentes.
- Drivers sem suporte → cair no `Introspect` simples.

Esses dados entram no prompt (`Relations`, `Indexes`) para que o LLM proponha
JOINs corretos e **evite sugerir índices que já existem**.

> Esta melhoria pode ser **Fase 2**; a Fase 1 funciona só com `Introspect`
> atual + EXPLAIN.

---

## 8. Segurança e cuidados

1. **Somente leitura por padrão**: `query-studio/explain` e `/preview` devem
   passar pelo parser de `drivers/safety.go` e **rejeitar** qualquer coisa que
   não seja `SELECT`/`EXPLAIN`. Índices sugeridos são **texto**, nunca
   executados pelo servidor.
2. **EXPLAIN ANALYZE executa a query de verdade**: exigir **confirmação
   explícita** na UI e um flag `analyze:true` no request. Aplicar timeout curto.
   Considerar bloquear `ANALYZE` para queries sem `LIMIT`/potencialmente caras
   (heurística) e/ou torná-lo desabilitável por configuração.
3. **Limite forçado no preview**: `LIMIT` imposto pelo backend (ex.: 100),
   independente do que veio do LLM. Timeout de execução curto.
4. **Roles**: endpoints sob `RequireAuth("admin","editor")` (mesma fronteira do
   fluxo de tools). `viewer` não acessa.
5. **Cripto de conexão**: reutilizar `openDriver` (decripta config com AAD
   `connection:<id>`). Nada de expor DSN/segredos na resposta.
6. **Injeção**: a query roda **parametrizada/como está** apenas em EXPLAIN e
   preview sandbox; jamais interpolar entrada do usuário em SQL de
   introspecção. Manter todas as verificações de `safety.go`.
7. **LLM**: não enviar dados sensíveis/PII de linhas ao OpenRouter — enviar
   **apenas metadados de schema** (tabelas/colunas/FKs/índices) e o plano de
   EXPLAIN (que não contém dados de linha, só custos/estruturas). O preview de
   linhas fica **no cliente**, não é reenviado ao LLM por padrão.
8. **Auditoria**: registrar em `audit_logs` ações relevantes (ex.: execução de
   EXPLAIN ANALYZE), seguindo o padrão de `handlers_backup.go::audit`.
9. **Logs**: não logar query texts com PII nem segredos.

---

## 9. Modelo de dados (persistência opcional — Fase 2)

Se quisermos histórico de sessões de query por usuário:

Migration nova: `store/migrations/0005_query_sessions.{up,down}.sql`
(lembrar de **incluir o `down.sql`**).

```sql
-- up
CREATE TABLE query_sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id UUID NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    query_text    TEXT NOT NULL DEFAULT '',
    chat_log      JSONB NOT NULL DEFAULT '[]'::jsonb,
    last_explain  JSONB,                 -- último plano de execução
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_query_sessions_created_by ON query_sessions(created_by);
```

- Model correspondente em `internal/store/models.go` (`QuerySession`).
- Usar `nullableJSON` para colunas JSONB opcionais.
- **Fora do escopo do backup** por ora (como tokens/usuários), a menos que se
  decida o contrário — documentar a decisão.

> Nota: a Fase 1 pode manter todo o estado **no cliente** (sem persistência),
> reduzindo o escopo inicial.

---

## 10. Fases de entrega (micro-fases para o autopilot)

Cada micro-fase abaixo é dimensionada para **uma única execução do Claude**
(contexto limpo), termina com **testes verdes** e **um commit**. A coluna
"Depende de:" declara apenas dependências reais (para permitir paralelismo).
As fases marcadas com o gate extra `integration-postgres` precisam da suíte de
integração com testcontainers (ver §11 e a lista de gates do autopilot).

---

### Fase 1a — Prompt + método LLM `QueryStudioChat`
**Meta:** produzir o contrato de I/O e o system prompt do assistente de query
(dialeto-aware, foco em performance, saída JSON), com método público no
`llm.Client` análogo a `ChatTool`, sem tocar em rotas/UI.

- [x] Definir `QueryStudioInput`/`QueryStudioOutput` em `internal/llm/prompts.go`.
- [x] Escrever o system prompt (regras de performance + guias por dialeto da §5.2).
- [x] Implementar `func (c *Client) QueryStudioChat(ctx, QueryStudioInput) (QueryStudioOutput, error)` (usar `response_format: json_object`).
- [x] Teste unitário de parse da saída JSON (padrão dos testes atuais de `llm`).

Depende de: —
**Testes:** `go build ./...`, `go test ./internal/llm/...` (parse do JSON de saída, campos preenchidos, resiliência a JSON malformado).

---

### Fase 1b — Capacidade EXPLAIN (interface + Postgres) e safety SELECT-only
**Meta:** introduzir a capacidade opcional `Explainer` no contrato de driver,
implementá-la no Postgres e garantir que EXPLAIN/preview só aceitem SELECT.

- [x] Adicionar `Explainer`/`ExplainResult` e `ErrUnsupported` em `internal/drivers/driver.go`.
- [x] Implementar `Explain` no driver `pg` (`EXPLAIN (FORMAT JSON)` e `ANALYZE, BUFFERS` quando `analyze=true`).
- [x] Reforçar `drivers/safety.go` para validar SELECT-only em EXPLAIN/preview (rejeitar DDL/DML).
- [x] Demais drivers retornam `ErrUnsupported`.
- [x] Testes: estender `drivers/safety_test.go` (SELECT-only) + teste do formato do comando EXPLAIN gerado.

Depende de: —
**Testes:** `go build ./...`, `go test ./internal/drivers/...`. Validação real do EXPLAIN Postgres via gate extra `integration-postgres`.
Gate extra: `integration-postgres`

---

### Fase 1c — Handlers `/query-studio/*` + registro de rotas
**Meta:** expor `/chat`, `/explain` e `/preview` no grupo admin+editor,
costurando 1a (LLM) e 1b (Explainer/safety), com `LIMIT` forçado no preview.

- [x] Criar `internal/api/handlers_query_studio.go` (`QueryStudioChat`, `QueryStudioExplain`, `QueryStudioPreview`).
- [x] Reusar `writeJSON`/`writeErr`/`decodeBody`/`currentUser`/`openDriver`.
- [x] Preview com `LIMIT` **imposto pelo servidor** (ex.: 100) + timeout curto; ignorar limite do LLM.
- [x] EXPLAIN ANALYZE exige flag `analyze:true` + auditoria em `audit_logs`.
- [x] Registrar as rotas em `handlers_llm_router.go::Mount` sob `RequireAuth("admin","editor")`.
- [x] Teste de handler (SELECT-only enforcement + LIMIT forçado; mockar driver/LLM).

Depende de: Fase 1a, Fase 1b
**Testes:** `go build ./...`, `go test ./internal/api/...` (auth, SELECT-only, LIMIT forçado, flag analyze).

---

### Fase 1d — Página `QueryStudioPage` (MVP UI + loop de refino)
**Meta:** entregar a página funcional (3 colunas) que consome `/chat`,
`/explain`, `/preview`, com seleção de conexão, schema via `Introspect` atual,
editor SQL e o loop de refino (reenvio do `ExplainResult` ao LLM).

- [x] Criar `frontend/src/pages/QueryStudioPage.tsx` (layout de 3 colunas da §6.2).
- [x] Menu em `App.tsx` (`links`) + rota em `main.tsx` sob `RequireAuth` admin+editor.
- [x] Queries TanStack: `["conns"]`, introspecção sob demanda; mutations de chat/explain/preview.
- [x] Botões: EXPLAIN, EXPLAIN ANALYZE (com confirmação), Preview, Copiar, Exportar `.sql`.
- [x] Loop de refino: enviar `ExplainResult` de volta ao `/chat`.

Depende de: Fase 1c
**Testes:** `./node_modules/.bin/tsc --noEmit -p .`, `npm run build`.

---

### Fase 2a — Introspecção rica (`IntrospectRich`) Postgres + MySQL no prompt
**Meta:** enriquecer o schema com FKs e índices e alimentar o prompt para que o
LLM proponha JOINs corretos e evite sugerir índices já existentes.

- [x] Adicionar `RichIntrospector`/`SchemaGraph`/`Relation`/`IndexInfo` (§7).
- [x] Implementar `IntrospectRich` para `pg` (`pg_constraint`, `pg_index`) e `mysql` (`KEY_COLUMN_USAGE`, `STATISTICS`).
- [x] Drivers sem suporte caem no `Introspect` simples.
- [x] Passar `Relations`/`Indexes` para `QueryStudioInput`; endpoint/handler consome a introspecção rica quando disponível.
- [x] Testes de parse dos catálogos + fallback.

Depende de: Fase 1a, Fase 1c
**Testes:** `go build ./...`, `go test ./internal/drivers/...`. Validação real via gate extra `integration-postgres`.
Gate extra: `integration-postgres`

---

### Fase 2b — Persistência de sessões (migration + endpoints + UI histórico)
**Meta:** salvar/listar sessões de query por usuário (título + query + chat +
último plano), com paginação e UI de histórico.

- [x] Migration `store/migrations/0005_query_sessions.{up,down}.sql` (incluir `down.sql` e testar rollback).
- [x] Model `QuerySession` em `internal/store/models.go`; usar `nullableJSON` nas colunas JSONB.
- [x] Endpoints `GET/POST /sessions`, `GET/DELETE /sessions/{id}` (paginação `page/page_size/q`).
- [x] UI de histórico com `Pagination`; carregar sessão salva na página.
- [x] Documentar decisão: sessões **fora do escopo do backup** (como tokens/usuários).
- [x] Testes de handler das sessões.

Depende de: Fase 1c, Fase 1d
**Testes:** `go build ./...`, `go test ./internal/api/...`, `./node_modules/.bin/tsc --noEmit -p .`, `npm run build`. Rollback/migração validados via gate extra `integration-postgres`.
Gate extra: `integration-postgres`

---

### Fase 2c — `Explainer` para MySQL, MSSQL, Oracle e Firebird
**Meta:** ampliar a cobertura de EXPLAIN aos demais dialetos suportados,
seguindo a matriz de sintaxe da §5.3.

- [x] Implementar `Explain` em `mysql` (`EXPLAIN FORMAT=JSON`; `ANALYZE` 8.0.18+).
- [x] Implementar `Explain` em `mssql`, `oracle`, `firebird` conforme §5.3 (ou `ErrUnsupported` documentado quando inviável).
- [x] Mongo/REST permanecem `ErrUnsupported`.
- [x] Testes do formato do comando por dialeto.

Depende de: Fase 1b
**Testes:** `go build ./...`, `go test ./internal/drivers/...`.
**Observação:** validação end-to-end de MSSQL/Oracle/Firebird depende de instâncias reais desses bancos (não cobertas por testcontainers padrão); cobrir por teste de geração de comando + validação manual quando houver ambiente.

---

### Fase 3a — Botão "Promover a Tool"
**Meta:** ponte entre Query Studio e o fluxo de Tools, pré-preenchendo a criação
de tool a partir da query atual.

- [x] Ação/atalho na `QueryStudioPage` que abre o fluxo de criação de tool com query/explicação pré-preenchidas.
- [x] Reusar rotas/estado existentes de `ToolsPage`.

Depende de: Fase 1d
**Testes:** `./node_modules/.bin/tsc --noEmit -p .`, `npm run build`.

---

### Fase 3b — Comparação de planos (antes/depois de índice sugerido)
**Meta:** permitir comparar o plano de execução antes e depois de aplicar um
índice sugerido, evidenciando o ganho estimado.

- [x] UI para armazenar e comparar dois `ExplainResult` (baseline vs. proposto).
- [x] Suporte no handler/`/explain` para retornar plano comparável (se necessário).

Depende de: Fase 1d, Fase 2a
**Testes:** `./node_modules/.bin/tsc --noEmit -p .`, `npm run build`, `go build ./...`, `go test ./internal/api/...` (se o backend mudar).

---

### Fase 3c — Detecção visual de anti-padrões
**Meta:** destacar na UI os anti-padrões reportados em `performance_notes`
(SELECT *, função em coluna indexada, OFFSET alto, etc.).

- [ ] Renderização visual dos `performance_notes`/`assumptions` com destaque (`Badge`/cores).
- [ ] Marcadores no editor/painel de notas.

Depende de: Fase 1d
**Testes:** `./node_modules/.bin/tsc --noEmit -p .`, `npm run build`.

---

### Fase 3d — Config para habilitar/desabilitar `EXPLAIN ANALYZE`
**Meta:** tornar o `EXPLAIN ANALYZE` desabilitável por configuração (defesa em
profundidade sobre a confirmação da UI).

- [ ] Flag em `internal/config/config.go` (ex.: `QUERY_STUDIO_ALLOW_ANALYZE`).
- [ ] Handler `/explain` respeita a flag (rejeita `analyze:true` quando desligado).
- [ ] UI oculta/desabilita o botão conforme capacidade retornada.
- [ ] Teste de handler para a flag.

Depende de: Fase 1c
**Testes:** `go build ./...`, `go test ./internal/api/...`, `./node_modules/.bin/tsc --noEmit -p .`, `npm run build`.

---

## 10.1 Grafo de dependências (resumo)

```
1a ─┐
    ├─> 1c ─> 1d ─┬─> 2b
1b ─┘            ├─> 3a
1b ──────> 2c    ├─> 3c
1a,1c ───> 2a ───┴─> 3b (com 1d)
1c ─────────────> 3d
```
Paralelizáveis desde o início: **1a** e **1b** (independentes). Após 1c/1d,
**2a/2b/2c/3d** podem correr em paralelo; a família Fase 3 depende da UI (1d).

---

## 11. Testes (obrigatório antes de finalizar — ver AGENT.md §7)

- **Backend**:
  - `go build ./...` e `go test ./...`.
  - Teste unitário do prompt/parse de saída (`llm`), no padrão dos testes
    atuais.
  - Teste do parser de segurança para EXPLAIN/preview (garantir SELECT-only),
    estendendo `drivers/safety_test.go`.
  - EXPLAIN por dialeto: teste de integração via testcontainers (Postgres),
    criar infra se necessário (documentar comando no PR).
- **Frontend**:
  - `./node_modules/.bin/tsc --noEmit -p .` e `npm run build`.
- **Regra de ouro**: não finalizar com build/tsc/test falhando; não remover
  testes existentes.

---

## 12. Arquivos a criar/editar (mapa rápido)

| Ação | Arquivo |
| --- | --- |
| Prompt + método LLM | `backend/internal/llm/prompts.go` (e `openrouter.go` se necessário) |
| Handlers do módulo | `backend/internal/api/handlers_query_studio.go` (novo) |
| Registro de rotas | `backend/internal/api/handlers_llm_router.go` (`Mount`) |
| Capacidade EXPLAIN | `backend/internal/drivers/driver.go` (interface) + cada `drivers/<db>/driver.go` |
| Introspecção rica (F2) | `backend/internal/drivers/*` (`IntrospectRich`) |
| Migration (F2) | `backend/internal/store/migrations/0005_query_sessions.{up,down}.sql` |
| Model (F2) | `backend/internal/store/models.go` |
| Página | `frontend/src/pages/QueryStudioPage.tsx` (novo) |
| Menu | `frontend/src/App.tsx` (`links`) |
| Rota | `frontend/src/main.tsx` |
| HTTP helper | `frontend/src/auth.tsx` (reuso de `api()`) |

---

## 13. Riscos e mitigação

| Risco | Mitigação |
| --- | --- |
| `EXPLAIN ANALYZE` executa query cara | Confirmação explícita + timeout + heurística de bloqueio p/ queries sem limite |
| LLM sugerir DDL/DML executável | Índices como texto; execução só de SELECT/EXPLAIN; parser `safety.go` |
| Envio de PII ao LLM | Enviar só metadados de schema + plano; preview de linhas só no cliente |
| Divergência de dialeto | Guias por dialeto no prompt + EXPLAIN específico por driver |
| Escopo crescer demais | Faseamento; Fase 1 sem persistência e só Postgres |

---

_Última revisão do plano: micro-fases para o autopilot + Registro de Andamento._

---

## 14. Registro de Andamento

> Memória compartilhada entre execuções do autopilot. **Cada fase**, ao
> concluir, adiciona uma entrada aqui com: o que foi feito, decisões tomadas,
> descobertas relevantes para as próximas fases (nomes de funções/arquivos
> reais, gotchas, dívidas) e o hash do commit. Não apague entradas anteriores.

Formato sugerido por entrada:

```
### <fase> — <título curto> (<data>)
- **Feito:** ...
- **Decisões:** ...
- **Descobertas / para as próximas fases:** ...
- **Testes:** <comandos rodados + resultado>
- **Commit:** <hash>
```

<!-- As entradas do autopilot começam abaixo desta linha. -->

### Fase 1a — Prompt + método LLM `QueryStudioChat` (2026-07-03)
- **Feito:**
  - Em `backend/internal/llm/prompts.go`, adicionei os tipos `QueryStudioInput`
    e `QueryStudioOutput`, o system prompt `queryStudioSystem` (engenheiro de
    banco sênior, foco em performance, SELECT-only, saída JSON estrita), o
    helper `queryStudioDialectGuidance` (guias por dialeto: pg/mysql/mssql/
    oracle/firebird) e o método público `func (c *Client) QueryStudioChat(ctx,
    QueryStudioInput) (*QueryStudioOutput, error)` — análogo a `ChatTool`,
    usando `c.Chat(ctx, msgs, true)` (que já seta `response_format: json_object`).
  - Extraí `parseQueryStudioOutput(raw string)` para permitir teste de parse
    sem LLM ao vivo (valida JSON, exige `reply` não-vazio, preenche `Raw`).
  - Novo arquivo de testes `backend/internal/llm/query_studio_test.go`
    (proposta completa, reply-only, JSON malformado, reply vazio rejeitado,
    guias de dialeto).
- **Decisões / desvios:**
  - **Omiti os campos `Relations []Relation` / `Indexes []IndexInfo`** que a §5.2
    do plano mostra no `QueryStudioInput`. Motivo: os tipos `Relation`/
    `IndexInfo`/`SchemaGraph`/`RichIntrospector` só são criados na **Fase 2a**
    (introspecção rica). Mantive a Fase 1a autocontida (compila sem depender de
    Fase 2). **Para a Fase 2a:** estender `QueryStudioInput` com esses campos e
    renderizá-los no prompt (o system prompt já orienta a evitar sugerir índices
    já existentes — basta alimentar os dados).
  - O prompt já contém o gancho do **loop de refino**: o campo `ExplainResult`
    é injetado como contexto ("Last execution plan (EXPLAIN) — use it to
    optimise"), truncado em 8000 chars. A Fase 1c/1d só precisam popular esse
    campo com o retorno do `/explain`.
  - `QueryStudioOutput` usa `omitempty` em todos os campos exceto `reply`; a UI
    (Fase 1d) deve tratar `query`/`explanation` vazios como "ainda coletando
    requisito" (turno conversacional puro).
- **Descobertas / para as próximas fases:**
  - `llm.Client.Chat(ctx, msgs, true)` já aplica `response_format:json_object`
    e `Temperature: 0.1` (ver `openrouter.go`). Não precisa de novo método no
    client HTTP.
  - Helpers reutilizáveis já existentes em `prompts.go`: `formatTableForPrompt`
    (omite schema sintético p/ firebird), `nullableTag`, `truncate`. Reusei
    todos. **Não havia** arquivo de teste em `internal/llm` antes desta fase —
    este é o primeiro (`query_studio_test.go`).
  - `drivers.Table{Schema, Name, Columns []Column{Name,Type,Nullable}}` é o
    shape do schema (em `drivers/driver.go`). O `ConnectionKind` esperado para
    Postgres é `"pg"` (a guidance também aceita "postgres"/"postgresql").
  - Sem chamadas de rede nos testes (parse puro) → roda no gate padrão sem
    OPENROUTER_API_KEY.
- **Testes:** `go build ./...` → OK; `go test ./...` → OK (llm 5 testes novos
  passando; backup/drivers inalterados). Frontend não tocado nesta fase.
- **Commit:** (a cargo do orquestrador)

### Fase 1b — Capacidade EXPLAIN (interface + Postgres) e safety SELECT-only (2026-07-03)
- **Feito:**
  - `internal/drivers/driver.go`: adicionei o sentinel `ErrUnsupported`, o tipo
    `ExplainResult{Dialect,Plan,Format,Analyze}`, a interface opcional
    `Explainer` (`Explain(ctx, query, analyze) (*ExplainResult, error)`) e um
    **helper de pacote** `drivers.Explain(ctx, d Driver, query, analyze)` que
    faz o type-assert e devolve `ErrUnsupported` para drivers sem suporte. Esse
    helper é o **ponto de entrada único** que a Fase 1c deve usar.
  - `internal/drivers/safety.go`: nova função `EnforceSelectOnly(sql)` —
    variante mais estrita de `EnforceReadOnly` que aceita **apenas** `SELECT`/
    `WITH` e rejeita `SHOW`/`EXPLAIN`/DDL/DML. É reuso de `EnforceReadOnly`
    (mantém stripping de comentários, `;` final e multi-statement) + checagem
    do primeiro token.
  - `internal/drivers/pg/driver.go`: `buildExplainSQL(query, analyze)` (função
    pura, testável) que gera `EXPLAIN (FORMAT JSON) <q>` ou
    `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) <q>`; e o método
    `(*driver).Explain` que valida via `EnforceSelectOnly`, roda o EXPLAIN
    (single row / single col JSON via `QueryRow.Scan`) e devolve
    `ExplainResult{Dialect:"pg", Format:"json"}`.
  - Testes: `drivers/safety_test.go` (aceita SELECT/WITH; rejeita
    EXPLAIN/SHOW/DELETE/UPDATE/DROP/CREATE/INSERT/multi-stmt/vazio);
    `drivers/explain_test.go` (helper devolve `ErrUnsupported` p/ driver sem
    Explainer e delega corretamente quando implementa); `pg/driver_test.go`
    (formato do comando EXPLAIN, com e sem ANALYZE).
- **Decisões / desvios:**
  - **Padrão de capacidade opcional via type-assert** (não adicionei método
    `Explain` retornando `ErrUnsupported` em cada driver). O checkbox "demais
    drivers retornam `ErrUnsupported`" é satisfeito pelo helper
    `drivers.Explain`, que devolve `ErrUnsupported` quando o driver não
    implementa `Explainer`. Assim mongo/rest/mysql/mssql/oracle/firebird ficam
    intactos e a detecção de capacidade é limpa. **Fase 1c:** chame sempre
    `drivers.Explain(...)`, nunca faça type-assert por conta própria; trate
    `errors.Is(err, drivers.ErrUnsupported)` para desabilitar o botão na UI.
  - **`EnforceSelectOnly` separada de `EnforceReadOnly`**: a existente continua
    permitindo SHOW/EXPLAIN (usada pelo executor de tools). A nova é para o
    Query Studio, onde o backend é quem envelopa em EXPLAIN — então o input
    tem de ser um SELECT puro. **Fase 1c:** o handler `/preview` e `/explain`
    devem passar a query por `EnforceSelectOnly` (o driver `pg.Explain` já
    chama, mas o handler deve validar antes de forçar `LIMIT` no preview).
  - **Campo `Analyze bool` adicionado ao `ExplainResult`** (não previsto na
    §5.3 do plano, que só tinha Dialect/Plan/Format). Útil para a UI e
    auditoria saberem se a query foi de fato executada. Serializa como JSON.
  - **Timeout do ANALYZE**: `Explain` respeita o `context` recebido — não tem
    parâmetro próprio de timeout. **Fase 1c:** o handler deve impor
    `context.WithTimeout` curto antes de chamar (especialmente com
    `analyze=true`), conforme §8.2.
  - `pg.Explain` usa `QueryRow.Scan(&plan)` num `string` — com FORMAT JSON o
    Postgres devolve **uma** linha, **uma** coluna `json` contendo o array do
    plano. Como o driver usa `QueryExecModeSimpleProtocol`, o valor chega como
    texto e o scan em `string` funciona.
- **Descobertas / para as próximas fases:**
  - **Não havia infra de testcontainers** (`go.mod` sem dependência; nenhum
    build tag `integration`). Em vez de introduzir Docker/testcontainers,
    criei `pg/driver_integration_test.go` como **teste que se auto-pula**
    quando `QUERY_STUDIO_TEST_PG_DSN` não está setado — fica verde no gate
    padrão (`go test ./...`) e valida o EXPLAIN real (plano é JSON válido,
    ANALYZE reporta "Actual", DROP é rejeitado) quando o gate
    `integration-postgres` fornece a DSN. **Comando:**
    `QUERY_STUDIO_TEST_PG_DSN="postgres://user:pass@host:5432/db?sslmode=disable" go test ./internal/drivers/pg/ -run TestExplain_Integration -v`.
    **Fases 2a/2c:** reusar essa mesma convenção de env-var para novos testes
    de integração Postgres (ex.: `IntrospectRich`).
  - `pg.Config{DSN string}` é o shape do config do driver (JSON). `pg.New(cfg
    []byte)` constrói. `d.Close()` fecha o pool.
  - A ordem dos tokens de `EnforceReadOnly` já permitia EXPLAIN/SHOW — por isso
    a nova função **não substitui** a antiga, só endurece para o novo módulo.
- **Testes:** `go build ./...` → OK; `go vet ./internal/drivers/...` → OK;
  `go test ./...` → OK (drivers: +2 safety, +2 explain helper, +1 pg
  buildExplainSQL; integration pg pula sem DSN; llm/backup inalterados).
  Frontend não tocado nesta fase.
- **Commit:** (a cargo do orquestrador)
- **Correção pós-gate (2026-07-03):** o gate extra `integration-postgres`
  falhava com `pattern ./...: directory prefix . does not contain main module`.
  **Causa raiz:** em `automacao/autopilot.json`, o gate `integration-postgres`
  não declarava `"dir"`, então `go test -tags=integration ./...` rodava na raiz
  do repositório — onde não há `go.mod` (o módulo vive em `backend/`). Não era
  problema de código nem de teste. **Correção:** adicionei `"dir": "backend"`
  ao gate `integration-postgres`, igualando a convenção do gate `backend`
  (todos os comandos Go rodam em `backend/`). Nenhum teste foi
  desabilitado/skipado. Gates re-executados a partir de `backend/`:
  `go build ./...` OK, `go test ./...` OK, `go test -tags=integration ./...` OK
  (o `pg/driver_integration_test.go` continua se auto-pulando sem
  `QUERY_STUDIO_TEST_PG_DSN`, como projetado).

### Fase 1b — Re-execução (código reimplementado) (2026-07-03)
- **Contexto / por que reabriu:** o commit anterior `bf6ad64`
  ("chore(praxis): estado apos Fase 1b [falhou]") **só continha edições de
  automação** (`PLANO.md`, `autopilot.json`, `fases.csv`) — `git show --stat`
  confirma que **nenhum arquivo `.go` foi commitado**. O código descrito na
  entrada anterior (Explainer/ExplainResult/EnforceSelectOnly/pg.Explain +
  testes) **tinha sido perdido/revertido** e não existia na árvore. O gate
  vermelho registrado no log era, na verdade, o problema de `dir` do gate
  `integration-postgres`, já corrigido em `autopilot.json` (`"dir": "backend"`
  presente). Esta execução **reimplementou o código do zero**, fiel à entrada
  anterior, e confirmou todos os gates verdes.
- **Feito (reimplementado, idêntico ao especificado):**
  - `internal/drivers/driver.go`: `ErrUnsupported` (sentinel), tipo
    `ExplainResult{Dialect,Plan,Format,Analyze}`, interface opcional
    `Explainer` e o helper de pacote `drivers.Explain(ctx, d, query, analyze)`
    (type-assert → `ErrUnsupported` quando o driver não implementa). **Ponto de
    entrada único** para a Fase 1c.
  - `internal/drivers/safety.go`: `EnforceSelectOnly(sql)` — reusa
    `EnforceReadOnly` e depois exige primeiro token `SELECT`/`WITH` (rejeita
    `SHOW`/`EXPLAIN`/DDL/DML). É a validação para o Query Studio (o backend é
    quem envelopa em EXPLAIN).
  - `internal/drivers/pg/driver.go`: `buildExplainSQL(query, analyze)` (função
    pura testável) → `EXPLAIN (FORMAT JSON) <q>` ou
    `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) <q>`; método `(*driver).Explain`
    que valida via `EnforceSelectOnly`, roda o EXPLAIN e devolve
    `ExplainResult{Dialect:"pg", Format:"json"}`.
  - Testes: `safety_test.go` (+2: aceita SELECT/WITH; rejeita
    EXPLAIN/SHOW/DELETE/UPDATE/DROP/CREATE/INSERT/multi-stmt/vazio);
    `drivers/explain_test.go` (novo: helper devolve `ErrUnsupported` p/ driver
    sem Explainer via `errors.Is`, e delega corretamente quando implementa —
    usa driver-stub `baseDriver`/`explainDriver`);
    `pg/driver_test.go` (novo: formato do comando EXPLAIN com/sem ANALYZE);
    `pg/driver_integration_test.go` (novo, build tag `//go:build integration`,
    auto-pula sem `QUERY_STUDIO_TEST_PG_DSN`).
- **Decisões / desvios:** mantidas as mesmas da entrada anterior (capacidade
  opcional via type-assert; `EnforceSelectOnly` separada de `EnforceReadOnly`;
  campo `Analyze bool` no `ExplainResult`; timeout do ANALYZE é
  responsabilidade da Fase 1c via `context.WithTimeout`). **Nenhum teste
  existente removido/desabilitado.**
- **Descobertas / para as próximas fases:**
  - **Gate `integration-postgres` já corrigido:** em `automacao/autopilot.json`
    o gate tem `"dir": "backend"`. `go test -tags=integration ./...` roda a
    partir de `backend/` e passa (o teste de integração pg auto-pula).
  - **Lição para o orquestrador:** o estado "[falhou]" commitou apenas a
    documentação, sem o código — cuidado ao confiar só no Registro anterior;
    verifique a árvore (`git show --stat`) antes de assumir que uma fase está
    implementada.
  - Go local disponível via PowerShell (`go1.26.4`); o Bash tool **não** tem
    `go` no PATH — use PowerShell para comandos Go nesta máquina.
- **Testes:** `go build ./...` OK; `go vet ./internal/drivers/...` OK;
  `go test ./...` OK; `go test -tags=integration ./...` OK (integração pg
  auto-pula sem DSN). Frontend não tocado (gate `somente_se_mudou` não roda).
- **Commit:** (a cargo do orquestrador)

### Fase 1c — Handlers `/query-studio/*` + registro de rotas (2026-07-03)
- **Feito:**
  - Novo `backend/internal/api/handlers_query_studio.go` com os três handlers:
    - `QueryStudioChat` — costura a Fase 1a. Decodifica `{connection_id,
      tables, messages, current_query, explain_result}`, exige `messages`,
      exige `a.LLM != nil` (503 se não configurado), resolve `kind` via
      `SELECT type FROM connections` (só quando `connection_id != uuid.Nil`) e
      chama `a.LLM.QueryStudioChat(...)`. Só envia **metadados de schema +
      plano EXPLAIN** ao LLM (nunca linhas) — as linhas do preview ficam no
      cliente (§8.7).
    - `QueryStudioExplain` — costura a Fase 1b. Valida `drivers.EnforceSelectOnly`
      **antes** de qualquer acesso a DB (400 em não-SELECT), exige
      `connection_id`, abre driver via `a.openDriver`, impõe
      `context.WithTimeout` (10s normal / 30s quando `analyze=true`, §8.2) e
      chama o **helper único** `drivers.Explain(ctx, drv, clean, analyze)`.
      `errors.Is(err, drivers.ErrUnsupported)` → **422** (UI desabilita o botão).
      `analyze=true` executa a query de verdade → grava em `audit_logs` via
      `a.audit(r, "query_studio.explain_analyze", connID, {dialect})`.
    - `QueryStudioPreview` — valida `EnforceSelectOnly` (400), exige
      `connection_id`, **impõe `LIMIT` no servidor** via `clampPreviewLimit`
      (máx 100, ignora o limite do LLM), timeout curto (15s) e chama
      `drv.Execute(...)` com `RowLimit`/`Timeout` forçados.
  - Rotas registradas em `handlers_llm_router.go::Mount`, no grupo
    `RequireAuth("admin","editor")`, logo após os `/llm/*`:
    `POST /query-studio/chat|explain|preview`.
  - Testes de handler em `handlers_query_studio_test.go` (novo — 1º teste do
    pacote `api`): `clampPreviewLimit` (LIMIT forçado), rejeição SELECT-only em
    preview e explain (DELETE/UPDATE/DROP/INSERT/TRUNCATE/**EXPLAIN cru**/**SHOW**/
    multi-statement/vazio → 400), SELECT válido passa o parser e para no guard
    de `connection_id`, `chat` exige messages (400) e exige LLM (503).
- **Decisões / desvios:**
  - **Handlers testáveis sem DB/LLM reais.** Como o pacote `api` não tinha
    infra de teste e `LLM`/`openDriver` são concretos (dependem de
    Pool+Cipher), estruturei os handlers para **validar query e campos
    obrigatórios ANTES de tocar em DB/driver/LLM**. Assim os testes rodam com um
    `&API{}` zero-value: os caminhos de validação (SELECT-only, connection_id
    ausente, messages ausentes, LLM nil) nunca chegam ao Pool. Não introduzi
    interfaces/mocks nem mexi em `api.go` — evita tocar em superfícies de outras
    fases. **Para a Fase 1d/2b:** o caminho "SELECT válido + connection real"
    (que chama `openDriver`/`Execute`/`Explain`) fica coberto pelo gate
    `integration-postgres` quando houver DSN — hoje não há mock de Pool.
  - **`clampPreviewLimit` como função pura** (const `queryStudioPreviewMaxRows =
    100`): retorna 100 para `<=0` **ou** `>100`; honra 1..100. É o ponto único
    de "LIMIT imposto pelo servidor". O campo `row_limit` do request é opcional
    (`omitempty`) e nunca confia no LIMIT que o LLM colocou na query.
  - **Timeouts como constantes no handler** (`queryStudioExplainTimeout=10s`,
    `queryStudioAnalyzeTimeout=30s`, `queryStudioPreviewTimeout=15s`) — a Fase
    1b deixou explícito que o `pg.Explain` respeita o `context` mas **não** impõe
    timeout próprio; o handler é quem aplica (§8.2). ANALYZE ganha janela maior
    porque executa a query, mas ainda bounded.
  - **422 (Unprocessable Entity) para `ErrUnsupported`** no `/explain` (não 501):
    mantém coerência com o resto do módulo (preview usa 422 para erro de
    execução) e a mensagem "explain not supported for this connection" permite à
    UI desabilitar o botão. **Fase 1d:** tratar 422 nesse endpoint como
    "capacidade ausente".
  - **Contrato de request** (nomes JSON reais para a Fase 1d): chat →
    `{connection_id, tables, messages, current_query, explain_result}`;
    explain → `{connection_id, query, analyze}`; preview →
    `{connection_id, query, row_limit?}`. `messages` usa `llm.Message`
    (`{role, content}`); `tables` usa `drivers.Table`. A resposta do `/chat` é o
    `*llm.QueryStudioOutput` (campos `reply`, `query`, `explanation`,
    `suggested_indexes`, `performance_notes`, `assumptions`; `Raw` tem tag `-`,
    não sai no JSON). `/explain` responde `drivers.ExplainResult`
    (`{dialect, plan, format, analyze}`); `/preview` responde
    `drivers.ExecResult` (`{columns, rows, count}`).
- **Descobertas / para as próximas fases:**
  - `a.openDriver(r, id)` (em `handlers_connections.go`) é o jeito canônico de
    abrir um driver ad-hoc (decripta config com AAD `connection:<id>`); o caller
    **deve** `defer drv.Close()`. Reusei-o nos dois handlers de execução.
  - `a.audit(r, action, target, details)` (em `handlers_backup.go`) já
    swallow-a erros e usa `currentUser(ctx)` para o actor — basta chamar. A
    ação nova é `"query_studio.explain_analyze"`.
  - Padrão do `ChatTool`: só resolve `kind` da conexão quando `connection_id !=
    uuid.Nil`; reaproveitei essa lógica no `QueryStudioChat` (permite conversa
    puramente conceitual sem conexão selecionada).
  - **Sem mock de `*pgxpool.Pool`** no repo: qualquer teste que precise exercer
    o caminho pós-validação (Execute/Explain reais) depende do gate
    `integration-postgres`. Se a Fase 2b/3d quiser testar handlers de DB de
    verdade sem Postgres, terá que introduzir uma interface sobre o Pool (não
    feito aqui para não ampliar escopo).
  - Go local disponível via PowerShell (`go` no PATH); o Bash tool **não** tem
    `go`. Frontend intocado nesta fase → gates `tsc`/`build` não rodam.
- **Testes:** `go build ./...` OK; `go vet ./internal/api/...` OK;
  `go test ./...` OK (pacote `api` agora com 7 testes novos, todos passando;
  demais pacotes inalterados); `go test -tags=integration ./...` OK (pg
  auto-pula sem DSN).
- **Commit:** (a cargo do orquestrador)

### Fase 1d — Página `QueryStudioPage` (MVP UI + loop de refino) (2026-07-03)
- **Feito:**
  - Nova página `frontend/src/pages/QueryStudioPage.tsx` com o layout de 3
    colunas da §6.2: **Schema** (árvore navegável de tabelas → colunas, com
    filtro; clicar em tabela/coluna insere a referência no chat), **Requisito**
    (chat livre em PT, histórico + input com Enter/Shift+Enter, renderiza
    `reply`, `assumptions` e a query proposta com botão "Usar esta query") e
    **SQL & Performance** (`CodeEditor` SQL com autocomplete alimentado pelo
    schema introspectado; Explicação, Índices sugeridos, Notas de performance;
    abas Plano de execução / Preview; botões EXPLAIN, EXPLAIN ANALYZE (com
    `window.confirm`), Preview, Copiar (clipboard), Exportar `.sql`).
  - Menu em `App.tsx`: novo ícone `IconQueryStudio` + entry
    `{ to: "/query-studio", label: "Query Studio" }` no grupo **Catálogo**,
    logo abaixo de **Tools** (§3). Rota em `main.tsx` sob `RequireAuth`
    (import de `QueryStudioPage` + `<Route path="/query-studio">`).
  - Queries/mutations TanStack: `["conns"]` → `/api/connections`; introspecção
    sob demanda → `/api/connections/{id}/introspect`; mutations para
    `/api/query-studio/{chat,explain,preview}`.
  - **Loop de refino:** o `plan` retornado pelo `/explain` é guardado em
    `pendingExplain` e enviado como `explain_result` na **próxima** mensagem de
    `/chat`, sendo limpo logo após (para não reenviar em loop). Um aviso visual
    ("Plano de execução será enviado ao assistente na próxima mensagem")
    aparece enquanto há plano pendente.
- **Decisões / desvios:**
  - **Gating de EXPLAIN por tipo de conexão no cliente.** A Fase 1c faz o
    `/explain` responder **422** quando o driver não implementa `Explainer`
    (só `pg` na Fase 1b). Para não deixar o usuário clicar num botão que sempre
    falha, a UI desabilita EXPLAIN/EXPLAIN ANALYZE quando
    `conn.type` ∉ {`pg`,`postgres`,`postgresql`} (`EXPLAIN_KINDS`) e mostra um
    `Badge` "EXPLAIN indisponível". **Para a Fase 2c:** ao adicionar `Explain`
    a mysql/mssql/oracle/firebird, **incluir esses `type`s em `EXPLAIN_KINDS`**
    em `QueryStudioPage.tsx` (constante no topo do arquivo). O 422 continua
    sendo o fallback correto se a heurística e o backend divergirem.
  - **Auto-aplicação da proposta.** Diferente da `ToolsPage` (que só popula o
    form ao clicar "Usar esta query"), aqui a última proposta do assistente é
    **auto-aplicada** ao editor SQL + painéis (Explicação/Índices/Notas), pois
    o Query Studio é um workspace de **uma query só** — isso torna o loop
    (descrever → EXPLAIN → refinar) mais fluido. O botão "Usar esta query"
    permanece em cada proposta do histórico para reaplicar uma versão anterior.
  - **Preview envia só `{connection_id, query}`** (sem `row_limit`) — o backend
    impõe o LIMIT (100) via `clampPreviewLimit`. As linhas do preview ficam
    **no cliente** e **nunca** são reenviadas ao `/chat` (§8.7); só o plano de
    EXPLAIN volta ao LLM.
  - **Sem role-guard de rota.** O projeto não tem componente de proteção por
    role em `main.tsx` (só `RequireAuth` de autenticação; visibilidade fina é
    por menu, como já ocorre com Tools). Mantive esse padrão — a fronteira
    admin+editor é garantida no **backend** (Fase 1c). A `Fase 2b/3a` que
    precisar esconder itens por role deve seguir o padrão de `adminGroup` em
    `App.tsx`.
  - **Contrato consumido (nomes reais):** `/chat` recebe
    `{connection_id, tables, messages:[{role,content}], current_query,
    explain_result}` e responde `{reply, query, explanation, suggested_indexes,
    performance_notes, assumptions}`. `/explain` recebe
    `{connection_id, query, analyze}` e responde
    `{dialect, plan, format, analyze}`. `/preview` recebe
    `{connection_id, query}` e responde `{columns, rows, count}`.
- **Descobertas / para as próximas fases:**
  - `CodeEditor` (`components/CodeEditor.tsx`) aceita `sqlSchema: Record<string,
    string[]>` (mapa `schema.table`/`table` → colunas) para autocomplete —
    reusei a mesma derivação da `ToolsPage`. `EmptyState` **não** aceita
    `children`: usar as props `title`/`description`.
  - `conns.data[].type` guarda o tipo do driver (ex.: `"pg"`, `"mysql"`); é a
    fonte usada para decidir suporte a EXPLAIN no cliente.
  - **Para a Fase 3a ("Promover a Tool"):** o estado da query pronta vive em
    `QueryStudioPage` como `query`/`explanation`/`suggestedIndexes`/
    `performanceNotes`/`assumptions` — reusar esses states para pré-preencher o
    fluxo da `ToolsPage`. **Para a Fase 3c (anti-padrões):** `performanceNotes`
    e `assumptions` já são renderizados como listas — basta adicionar destaque
    (`Badge`/cores). **Para a Fase 3d (config ANALYZE):** hoje a UI só oculta
    EXPLAIN por dialeto; quando o backend expuser a capacidade de ANALYZE
    (flag), condicionar o botão "EXPLAIN ANALYZE" a essa capacidade.
  - `npm run build` no Windows/PowerShell emite um `NativeCommandError`
    cosmético (PowerShell embrulha o stderr do vite com o aviso de chunk
    >500 kB); **não é falha** — o build conclui com `✓ built`. O aviso de
    tamanho de chunk é **pré-existente** (bundle único), não introduzido aqui.
- **Testes:** `./node_modules/.bin/tsc --noEmit -p .` → OK (sem erros);
  `npm run build` → OK (`tsc -b && vite build`, `✓ built`). Backend não tocado
  nesta fase → gates Go não requeridos para 1d.
- **Commit:** (a cargo do orquestrador)

### Fase 2a — Introspecção rica (`IntrospectRich`) Postgres + MySQL no prompt (2026-07-03)
- **Feito:**
  - Novo `internal/drivers/rich.go`: tipos `Relation` (FK composta:
    `FromColumns`/`ToColumns` são slices posicionais), `IndexInfo`
    (nome/schema/tabela/colunas/unique), `SchemaGraph{Tables,Relations,Indexes}`
    e a interface opcional `RichIntrospector{IntrospectRich(ctx) (*SchemaGraph,
    error)}`. Helper de pacote **`drivers.IntrospectRich(ctx, d)`** — type-assert
    para `RichIntrospector`; **fallback** para `d.Introspect` (Relations/Indexes
    vazios) quando o driver não implementa. É o **ponto de entrada único** (mesmo
    padrão de `drivers.Explain`). Helpers puros **`BuildRelations([]FKColumn)`** e
    **`BuildIndexes([]IndexColumn)`** que agrupam linhas de catálogo (já ordenadas
    por constraint/índice + ordinal) em entradas compostas — compartilhados por pg
    e mysql e testados isoladamente.
  - `pg/driver.go`: `IntrospectRich` reusa `Introspect` + `introspectRelations`
    (`pg_constraint` com `unnest(conkey,confkey) WITH ORDINALITY` p/ FKs
    compostas em ordem) + `introspectIndexes` (`pg_index` com
    `unnest(indkey) WITH ORDINALITY`; `attnum > 0` descarta colunas de índices de
    expressão). `var _ drivers.RichIntrospector = (*driver)(nil)`.
  - `mysql/driver.go`: `IntrospectRich` via
    `INFORMATION_SCHEMA.KEY_COLUMN_USAGE` (FKs; `REFERENCED_TABLE_NAME IS NOT
    NULL`, ordenado por tabela+constraint+ORDINAL_POSITION) e
    `INFORMATION_SCHEMA.STATISTICS` (índices; `NON_UNIQUE=0`→unique, ordenado por
    SEQ_IN_INDEX). `var _ drivers.RichIntrospector = (*driver)(nil)`.
  - `internal/llm/prompts.go`: `QueryStudioInput` ganhou
    `Relations []drivers.Relation` e `Indexes []drivers.IndexInfo`. Extraí
    **`buildQueryStudioContext(in)`** (função pura, testável) que renderiza as
    seções "Foreign keys (use these for JOINs)" e "Existing indexes (do NOT
    suggest an index that duplicates one of these)". Novo helper `qualifiedName`
    (schema.table, respeitando Firebird sem schema); `formatTableForPrompt` agora
    o reusa.
  - `internal/api/handlers_query_studio.go`: `QueryStudioChat` agora abre o driver
    (via `a.openDriver`) quando há `connection_id`, deriva `kind` de
    `drv.Kind()` e faz **introspecção rica best-effort** (novo timeout
    `queryStudioIntrospectTimeout=10s`), passando `Relations`/`Indexes` ao LLM.
    Só metadados de schema vão ao LLM (§8.7) — nunca linhas. Falha de DB/introspecção
    degrada para contexto schema-only (não quebra o chat).
  - Testes: `internal/drivers/rich_test.go` (BuildRelations single/composto/mesmo
    nome-de-constraint-em-tabelas-distintas; BuildIndexes unique+composto+vazio;
    IntrospectRich delega e faz fallback); `internal/llm/query_studio_test.go`
    (+3: render de FK/índice, Firebird sem schema, seções omitidas quando vazias);
    `pg/driver_integration_test.go` (+`TestIntrospectRich_Integration`: provisiona
    schema `qs_rich_test` parent/child+índice, valida FK e índice; auto-pula sem
    `QUERY_STUDIO_TEST_PG_DSN`).
- **Decisões / desvios:**
  - **Introspecção rica é server-side e best-effort no `/chat`**, não veio do
    cliente. Motivo: FKs/índices não estavam no payload do frontend (Fase 1d) e
    exigem o driver de qualquer forma; buscar no servidor mantém a Fase 2a
    **backend-only** (sem gate de frontend, coerente com os testes declarados).
    **Não** alterei o endpoint `GET /connections/{id}/introspect` (ainda devolve
    array de `Table`) para **não quebrar** a `QueryStudioPage`. **Para a Fase 3b/UI:**
    se quiser exibir FKs/índices na árvore de schema, exponha um endpoint rico
    novo (ex.: `?rich=1` ou rota dedicada) em vez de mudar o shape do atual.
  - **`RichIntrospector.IntrospectRich` retorna `*SchemaGraph`** (ponteiro), não o
    valor mostrado na §7 — consistência com `drivers.Explain`→`*ExplainResult` e
    permite `nil`.
  - **`Relation.FromColumns/ToColumns` são slices** (FKs compostas). Nomes de
    constraint **não são globalmente únicos** — em pg podem repetir entre schemas,
    em mysql entre tabelas — então `BuildRelations` agrupa por
    `(from schema, from table, constraint)` e `BuildIndexes` por
    `(schema, table, index name)`. Testes cobrem o caso "mesmo nome, tabelas
    distintas".
  - **Índices de expressão (pg) são ignorados** (`indkey` com attnum 0 não tem
    coluna). Suficiente para o objetivo (evitar sugerir índice já existente por
    coluna). **Fase 3b:** se precisar comparar planos com índices de expressão,
    estender a query.
  - **`kind` agora vem de `drv.Kind()`** (era `SELECT type FROM connections`). O
    valor é o mesmo string ("pg"/"mysql"); economiza um SELECT já que abrimos o
    driver de qualquer forma.
- **Descobertas / para as próximas fases:**
  - **Só `pg` e `mysql` implementam `RichIntrospector`.** mssql/oracle/firebird/
    mongo/rest caem no fallback (Relations/Indexes vazios) — sem erro. A Fase 2c
    (Explainer) é ortogonal a isto; se quiser introspecção rica nesses dialetos,
    é trabalho adicional não previsto na 2a.
  - **Contrato de `SchemaGraph` (JSON):** `{tables, relations:[{constraint_name,
    from_schema,from_table,from_columns[],to_schema,to_table,to_columns[]}],
    indexes:[{name,schema,table,columns[],unique}]}`. Reusável se a Fase 3b/UI
    precisar transportá-lo.
  - **Reuso para novos drivers:** implemente `IntrospectRich` escaneando as linhas
    de catálogo (ordenadas) para `[]drivers.FKColumn`/`[]drivers.IndexColumn` e
    chame `drivers.BuildRelations`/`BuildIndexes` — não reimplemente o
    agrupamento.
  - Go local disponível via PowerShell (`go` no PATH); Bash tool **não** tem `go`.
- **Testes:** `go build ./...` OK; `go vet ./internal/{drivers,llm,api}/...` OK;
  `go test ./...` OK (drivers: +7 rich; llm: +3 context; api inalterado passa);
  `go test -tags=integration ./...` OK (pg rich/explain auto-pulam sem
  `QUERY_STUDIO_TEST_PG_DSN`). Frontend não tocado.
- **Commit:** (a cargo do orquestrador)

### Fase 2b — Persistência de sessões (migration + endpoints + UI histórico) (2026-07-04)
- **Feito:**
  - **Migration `0005_query_sessions.{up,down}.sql`** (`internal/store/migrations`):
    tabela `query_sessions` (id, connection_id FK→connections ON DELETE CASCADE,
    title, query_text, chat_log JSONB DEFAULT '[]', last_explain JSONB nullable,
    created_by FK→users ON DELETE SET NULL, created_at/updated_at). Índices
    `idx_query_sessions_created_by(created_by, updated_at DESC)` e
    `idx_query_sessions_conn(connection_id)`. `down.sql` = `DROP TABLE IF EXISTS`.
  - **Model** `store.QuerySession` em `internal/store/models.go` (ChatLog/
    LastExplain como `json.RawMessage`; LastExplain com `omitempty`).
  - **Handlers** em novo `internal/api/handlers_query_sessions.go`:
    `ListQuerySessions` (GET, dual-mode legado/paginado com `page/page_size/q`,
    **escopado ao usuário** via `created_by = currentUser`, lista leve sem
    chat_log/last_explain, `ORDER BY updated_at DESC`), `SaveQuerySession`
    (POST — **cria OU atualiza** quando o body traz `id`; valida `title` e
    `connection_id` **antes** de tocar o DB; usa `nullableJSON(last_explain)` e
    default `'[]'` p/ chat_log; update escopado ao dono), `GetQuerySession`
    (GET `/{id}`, full row escopado ao dono → 404 se não for do usuário),
    `DeleteQuerySession` (DELETE `/{id}`, escopado, 204/404). Rotas registradas
    em `handlers_llm_router.go::Mount` sob `RequireAuth("admin","editor")`.
  - **Refactor** `internal/store/store.go`: extraí `toMigrateURL(connURL)`
    (rewrite `postgres://`→`pgx5://`) de `Migrate`, reusado pelo teste de rollback.
  - **Frontend** `QueryStudioPage.tsx`: botões **Salvar sessão** (usa
    `window.prompt` p/ título; salva connection_id + query + chat_log + last_explain;
    faz upsert com `currentSessionId`) e **Histórico** (overlay com busca,
    lista paginada server-side usando o componente `Pagination`, ações
    **Carregar**/**Excluir**). `loadSession` restaura connId, query, chat,
    explainResult e **re-aplica a última proposta do assistente** do chat_log
    para repovoar Explicação/Índices/Notas.
  - **Testes:** `handlers_query_sessions_test.go` (unit: title/connection_id
    obrigatórios → 400; id inválido em GET/DELETE → 400 — rodam com `&API{}`);
    `handlers_query_sessions_integration_test.go` (build tag `integration`,
    auto-pula sem DSN: CRUD completo create→list paged→get→update→delete→404,
    provisiona user+connection reais); `store/migrate_integration_test.go`
    (build tag `integration`, auto-pula: valida **rollback** de 0005 via
    `m.Steps(-1)`/`Steps(1)` conferindo `to_regclass('query_sessions')`).
- **Decisões / desvios:**
  - **POST faz upsert (create OU update)** em vez de adicionar rota `PUT`. O
    plano lista só GET/POST/DELETE; para a UI ter "salvar" e "atualizar" sem
    rota nova, `SaveQuerySession` atualiza quando o body traz `id` (escopado ao
    dono) e cria caso contrário. Retorna **201** no create e **200** no update.
  - **Tudo escopado ao usuário** (`created_by = currentUser`) em list/get/delete/
    update. "Sessões do usuário" (§4/§9) → um editor não vê/edita/apaga sessão de
    outro. A fronteira de role continua admin+editor (backend). **Para fases
    futuras:** se quiser sessões compartilhadas por org, relaxar esse filtro.
  - **Sessões FORA do escopo do backup** (decisão documentada, §9): não toquei em
    `internal/backup/backup.go`. Ficam como tokens/usuários — não exportadas nem
    restauradas. Não são versionadas (sem tabela `*_versions`).
  - **Lista leve:** `ListQuerySessions` devolve só id/connection_id/title/
    query_text/created_at/updated_at (sem chat_log/last_explain) p/ manter o
    payload do histórico enxuto; o detalhe (`GET /{id}`) traz o registro completo.
  - **Paginação server-side na UI:** o componente `Pagination` recebe props
    derivadas (`totalPages`/`start`/`end`) computadas do `total` retornado pela
    API — **não** usei `usePagination` (que faz slice client-side), pois a página
    já vem paginada do backend.
  - **`last_explain`/`chat_log` persistem só metadados** (plano de execução e
    transcript) — **nenhuma linha de preview** é salva (§8.7). O preview continua
    apenas no cliente.
- **Descobertas / para as próximas fases:**
  - **Nomes reais:** tabela `query_sessions`; rotas
    `GET/POST /api/query-studio/sessions` e `GET/DELETE /api/query-studio/sessions/{id}`.
    Contrato POST (upsert): `{id?, connection_id, title, query_text?, chat_log?,
    last_explain?}`. Resposta = `store.QuerySession`
    (`{id, connection_id, title, query_text, chat_log, last_explain?, created_by?,
    created_at, updated_at}`). Lista paginada = `{items, total, page, page_size}`.
  - **Sem mock de `*pgxpool.Pool`** (como notado na Fase 1c): os handlers que
    tocam DB são cobertos pelo teste `integration` (auto-pula sem
    `QUERY_STUDIO_TEST_PG_DSN`); os unit tests cobrem só validação pré-DB. Se uma
    fase futura precisar testar caminhos de DB sem Postgres, terá que introduzir
    uma interface sobre o Pool (não feito aqui).
  - **Convenção de integração reusável:** mesmo env-var `QUERY_STUDIO_TEST_PG_DSN`
    das Fases 1b/2a. O teste da `api` constrói `&API{Pool: pool}` e injeta o
    usuário no contexto via `context.WithValue(ctx, ctxKeyUserID, uid)` +
    `ctxKeyRole`; para `{id}` injeta um `chi.NewRouteContext()` em
    `chi.RouteCtxKey`. Reusar esse padrão para testar handlers com URL param.
  - **`store.toMigrateURL`** agora é o ponto único de rewrite de scheme p/ o
    golang-migrate (pgx5) — reusar em testes/ferramentas que precisem de um
    `*migrate.Migrate`.
  - **UI:** `EmptyState` usa props `title`/`description` (não children);
    `Pagination` espera props derivadas do total. O overlay de histórico fecha ao
    clicar no backdrop (`stopPropagation` no card).
  - Go local via PowerShell (`go` no PATH); Bash tool **não** tem `go`. `npm run
    build` emite `NativeCommandError` cosmético no PowerShell (stderr do vite) —
    não é falha; conclui com `✓ built`.
- **Testes:** `go build ./...` OK; `go vet ./internal/{api,store}/...` OK;
  `go test ./...` OK (api: +4 unit de sessões); `go test -tags=integration ./...`
  OK (novos testes de integração api/store auto-pulam sem DSN);
  `./node_modules/.bin/tsc --noEmit -p .` OK; `npm run build` OK (`✓ built`).
- **Commit:** (a cargo do orquestrador)

### Fase 2c — `Explainer` para MySQL, MSSQL, Oracle e Firebird (2026-07-04)
- **Feito:**
  - **MySQL** (`internal/drivers/mysql/driver.go`): `buildExplainSQL(query,
    analyze)` (função pura testável) → `EXPLAIN FORMAT=JSON <q>` (estimado, não
    executa; `Format:"json"`) ou `EXPLAIN ANALYZE <q>` (8.0.18+, executa;
    `Format:"text"`, plano em formato TREE). Método `(*driver).Explain` valida
    via `drivers.EnforceSelectOnly` e faz `QueryRowContext(...).Scan(&plan)` —
    ambos os EXPLAIN retornam 1 linha × 1 coluna. `var _ drivers.Explainer =
    (*driver)(nil)`.
  - **MSSQL** (`internal/drivers/mssql/driver.go`): `explainSettings(analyze)`
    (pura, testável) → par `SET SHOWPLAN_XML ON/OFF` (estimado, **não executa**)
    ou `SET STATISTICS XML ON/OFF` (real, **executa**). `(*driver).Explain`
    valida SELECT-only, **fixa uma conexão dedicada** (`d.db.Conn(ctx)`, pois os
    SET são de sessão), roda o `ON`, executa a query e coleta o XML via
    `scanShowplanXML` (itera result sets: drena os multi-coluna da própria query
    sob STATISTICS XML e captura o **último** result set de 1 coluna = o plano);
    `defer` sempre reseta com o `OFF` (via `context.Background`, para resetar
    mesmo se o ctx expirou) **antes** de devolver a conexão ao pool.
    `Format:"xml"`.
  - **Firebird** (`internal/drivers/firebird/driver.go`): `(*driver).Explain`
    retorna **`drivers.ErrUnsupported`** (documentado): o plano do Firebird só é
    exposto pelos comandos de cliente do isql (`SET PLAN`/`SET PLANONLY`); não há
    statement SQL de servidor (tipo EXPLAIN) que o driver wire-protocol possa
    emitir, e não há equivalente a EXPLAIN ANALYZE. Interface satisfeita
    explicitamente (`var _ drivers.Explainer`) para deixar a decisão testável.
  - **Oracle** (`internal/drivers/oracle/driver.go`): o driver é um **stub
    desabilitado** no build padrão (`New` sempre retorna `ErrNotEnabled`), então
    `(stub).Explain` retorna `ErrNotEnabled` (inalcançável na prática — nunca se
    constrói um driver oracle). Adicionei os builders puros
    `buildExplainPlanSQL(query)` (`EXPLAIN PLAN FOR <q>`) e `displayPlanSQL()`
    (`SELECT plan_table_output FROM TABLE(DBMS_XPLAN.DISPLAY())`) **documentando**
    a sintaxe (EXPLAIN PLAN é 2 passos no Oracle) para um build `-tags oracle`
    futuro; são exercidos por teste.
  - **Mongo/REST**: intocados — não implementam `Explainer`, então o helper
    `drivers.Explain` já devolve `ErrUnsupported`.
  - **Frontend** (`QueryStudioPage.tsx`): `EXPLAIN_KINDS` agora inclui `mysql` e
    `mssql` (os que têm EXPLAIN executável). **Não** inclui `firebird`/`oracle`
    (retornam ErrUnsupported/ErrNotEnabled → 422); o 422 continua sendo o
    fallback seguro se a heurística divergir do backend.
  - **Testes:** `mysql/driver_test.go` (formato do comando com/sem ANALYZE);
    `mssql/driver_test.go` (par de SET por modo); `oracle/driver_test.go`
    (builders + `Explain` → `ErrNotEnabled`); `firebird/driver_test.go`
    (`Explain` → `ErrUnsupported` nos dois modos).
- **Decisões / desvios:**
  - **Padrão do pg replicado**: comando em função pura testável +
    `EnforceSelectOnly` no início do `Explain`. A validação SELECT-only é
    **defesa em profundidade** — o handler da Fase 1c já valida antes, mas o
    driver revalida (mesma escolha do pg).
  - **MSSQL usa conexão dedicada + reset garantido.** SHOWPLAN/STATISTICS XML
    são opções de **sessão**; sem `Conn(ctx)` + `SET ... OFF` no defer, uma
    conexão do pool ficaria "presa" em modo showplan e **não executaria** as
    próximas queries. `scanShowplanXML` mantém o **último** valor de 1 coluna
    justamente porque, sob STATISTICS XML, o plano vem **depois** dos result sets
    da própria query. **Não validado contra SQL Server real** (sem instância);
    coberto por teste de geração de comando + validação manual quando houver
    ambiente (§2c "Observação").
  - **Firebird = ErrUnsupported explícito** em vez de "não implementar a
    interface": o resultado via helper é o mesmo, mas implementar deixa a decisão
    documentada no driver e testável. A UI já cai no 422.
  - **Oracle = stub → ErrNotEnabled.** Não há arquivo de driver Oracle real no
    repo (só o stub; `-tags oracle` não tem fonte correspondente aqui). Os
    builders ficam prontos e testados para quando o driver real for adicionado.
    **ErrNotEnabled ≠ ErrUnsupported**, mas é irrelevante no build padrão porque
    `oracle.New` falha antes de qualquer `Explain` (openDriver nunca retorna um
    driver oracle).
- **Descobertas / para as próximas fases:**
  - **Capacidade de EXPLAIN por dialeto (estado atual):** `pg` (JSON, estimado +
    ANALYZE), `mysql` (JSON estimado + ANALYZE/TREE text), `mssql` (XML, estimado
    via SHOWPLAN + real via STATISTICS). `firebird`/`oracle`/`mongo`/`rest` →
    sem EXPLAIN. **Fase 3b (comparação de planos):** o `plan` vem em formatos
    heterogêneos (`Format`: json/text/xml) — a UI de diff deve tratar por
    `format`, não assumir JSON.
  - **`drivers.ExplainResult.Format`** agora assume 3 valores: `"json"` (pg,
    mysql estimado), `"text"` (mysql analyze), `"xml"` (mssql). Considerar isso
    ao renderizar o plano.
  - **mssql `scanShowplanXML`** é um bom ponto de reuso se algum dia precisar ler
    múltiplos result sets de um `*sql.Rows` (usa `rows.NextResultSet()`).
  - **Sem novos testes de integração** nesta fase: MSSQL/Oracle/Firebird não têm
    testcontainers padrão; o gate `integration-postgres` não cobre esses
    dialetos. A validação end-to-end fica dependente de instâncias reais (§2c).
  - Go local disponível via PowerShell (`go` no PATH); Bash tool **não** tem
    `go`. `npm run build` no PowerShell conclui com `✓ built` (aviso de chunk é
    cosmético/pré-existente).
- **Testes:** `go build ./...` OK; `go vet ./internal/drivers/...` OK;
  `go test ./...` OK (novos: mysql +1, mssql +1, oracle +2, firebird +1;
  demais pacotes inalterados); `go build -tags=integration ./...` OK;
  `./node_modules/.bin/tsc --noEmit -p .` OK; `npm run build` OK (`✓ built`).
- **Commit:** (a cargo do orquestrador)

### Fase 3a — Botão "Promover a Tool" (2026-07-04)
- **Feito:**
  - **`QueryStudioPage.tsx`**: novo botão **"Promover a Tool"** no cabeçalho da
    coluna "SQL & Performance" (ao lado de Copiar / Exportar .sql), habilitado
    só quando há `query.trim()`. Handler `promoteToTool()` monta um objeto
    `PromoteToolState` (`{kind:"query", connection_id?, query_text, description?}`)
    e faz `navigate("/tools", { state: { promote } })` via `useNavigate` do
    `react-router-dom`. Não cria a tool — apenas leva o usuário ao fluxo de
    Tools já pré-preenchido. Exportei o **tipo** `PromoteToolState` do módulo
    (contrato compartilhado com a ToolsPage).
  - **`ToolsPage.tsx`**: consome `location.state.promote` (via `useLocation`).
    Um `useEffect([location.state])` faz o seed do form existente: `setF({
    ...INITIAL_FORM, kind:"query", connection_id, query_text, title, description })`,
    reseta `editingId`/`lastTest`/`tables`, injeta uma mensagem do assistente no
    chat ("Query importada do Query Studio…"), rola até o form (`formRef`) e
    **limpa o router state** (`navigate(pathname, { replace:true, state:null })`)
    para que refresh/voltar não reapliquem o prefill. Import de tipo:
    `import type { PromoteToolState } from "./QueryStudioPage"`.
- **Decisões / desvios:**
  - **Ponte via router state (não query params nem store global).** A `ToolsPage`
    guarda todo o form em `useState(INITIAL_FORM)` — não há store compartilhado.
    O caminho idiomático de React Router v6 (`navigate` com `state` + `useLocation`)
    reusa o **estado existente** do form sem introduzir Context/Redux/URL params,
    atendendo ao checkbox "reusar rotas/estado existentes de ToolsPage". Rotas
    inalteradas (`/tools` já existia em `main.tsx`).
  - **Prefill mínimo e seguro:** `query_text = query`, `description = explanation`,
    `connection_id = connId` (mesma tabela `connections` nos dois módulos → o id
    casa direto no dropdown da ToolsPage), `kind = "query"`. **Não** preenchi
    `slug`/`title`/`params_schema` — o usuário define slug (constraint
    `^[a-z0-9_]{1,64}$`) e parâmetros na ToolsPage; o `params_schema` fica no
    default `{"type":"object","properties":{}}`. `suggested_indexes`/
    `performance_notes` **não** são levados (são texto informativo do Query
    Studio; poluiriam o SQL/descrição). O dry-run da ToolsPage roda antes de
    ativar, como sempre.
  - **SQL preservado intacto:** não injetei comentários/índices no `query_text`
    para manter a query idêntica à validada por EXPLAIN no Query Studio.
- **Descobertas / para as próximas fases:**
  - **Contrato da ponte (nome real):** router state `{ promote: PromoteToolState }`,
    onde `PromoteToolState = { kind:"query"; connection_id?:string; query_text:string;
    title?:string; description?:string }` — exportado de `QueryStudioPage.tsx`.
    A `ToolsPage` limpa o state após aplicar (idempotente a refresh).
  - **Reuso para Fase 3b/3c:** o mesmo padrão `navigate(state)` + `useEffect(
    [location.state])` + limpeza serve para levar outros artefatos (ex.: plano
    baseline/proposto da 3b) entre páginas sem store global. `formRef` na
    ToolsPage já existe e é o ponto de scroll ao seedar.
  - **`INITIAL_FORM` da ToolsPage** é o baseline canônico do form; para pré-preencher
    faça spread sobre ele. `params_schema` default é
    `{"type":"object","properties":{}}` (string).
  - Frontend: `npm run build` no PowerShell conclui com `✓ built`; o aviso de
    chunk >500 kB é cosmético/pré-existente (bundle único). O `2>$null` no
    PowerShell evita o `NativeCommandError` cosmético do stderr do vite.
- **Testes:** `./node_modules/.bin/tsc --noEmit -p .` → OK (projeto inteiro, sem
  erros); `npm run build` → OK (`tsc -b && vite build`, `✓ built`). Backend não
  tocado nesta fase → gates Go não requeridos para 3a.
- **Commit:** (a cargo do orquestrador)

### Fase 3b — Comparação de planos (antes/depois de índice sugerido) (2026-07-04)
- **Feito:**
  - **`QueryStudioPage.tsx`** — comparação de dois `ExplainResult` inteiramente
    no cliente (nenhuma mudança de backend). Novos states `baselinePlan:
    ExplainResp | null` e `baselineNote: string`. O tipo `rightTab` ganhou o
    valor `"compare"` (além de `plan`/`preview`).
    - **Capturar baseline:** botão **"Definir como baseline"** (vira "Redefinir
      baseline") no cabeçalho de metadados do plano (aba *Plano de execução*),
      visível sempre que há um `explainResult`. `captureBaseline()` copia o
      `explainResult` atual para `baselinePlan` e, se houver **exatamente um**
      índice sugerido, pré-preenche `baselineNote` com ele.
    - **Aba "Comparação"** — aparece na barra de abas só quando há `baselinePlan`
      (com um `Badge` "baseline"). Renderiza o novo componente `PlanComparison`.
    - **`PlanComparison`** mostra: seletor do "Índice avaliado" (dropdown dos
      `suggestedIndexes`, ou input livre quando não há índices sugeridos, ligado a
      `baselineNote`); tabela de métricas **Baseline × Atual × Variação (%)**
      (verde = melhora/custo menor, vermelho = piora); e os **dois planos lado a
      lado** (`grid md:grid-cols-2`). Botão **"Limpar baseline"** (`clearBaseline`,
      volta para a aba `plan`).
    - **Fluxo de uso (documentar p/ usuários):** rodar EXPLAIN → "Definir como
      baseline" → aplicar o índice sugerido **manualmente no banco** (o módulo
      **nunca** executa DDL, §8.1) → rodar EXPLAIN de novo → abrir "Comparação".
      Enquanto `baselinePlan` existe mas ainda não há `explainResult` novo,
      um aviso âmbar orienta o próximo passo.
  - **Helpers puros exportados** (no rodapé do arquivo, ao lado de `formatCell`):
    - `extractPlanMetrics(plan, format)` — extrai `{totalCost, planRows,
      actualTime, actualRows}` **best-effort** e **só de planos `format:"json"`**.
      Postgres: raiz array/obj com nó `Plan` → `Total Cost`/`Plan Rows`/`Actual
      Total Time`/`Actual Rows`. MySQL `FORMAT=JSON`: `query_block.cost_info.
      query_cost` (+ `rows_examined_per_scan`/`rows_produced_per_join`). Retorna
      `null` para `text` (MySQL ANALYZE/TREE) e `xml` (MSSQL) → nesses casos a UI
      cai no **diff textual lado a lado** (sem % de variação).
    - `deltaPct(baseline, current)` — variação percentual sinalizada (negativo =
      melhora); `null` quando incomparável (baseline 0/ausente).
- **Decisões / desvios:**
  - **Backend NÃO mudou (checkbox "se necessário" = não necessário).** O
    `/explain` já devolve o plano completo e comparável (`ExplainResult{dialect,
    plan,format,analyze}`); o mesmo endpoint gera o baseline e o plano pós-índice.
    Toda a comparação é client-side → **gates Go não requeridos** para 3b (rodei
    `go build ./...` mesmo assim: OK). Isso mantém a fase alinhada ao aviso da
    Fase 2c: **formatos de plano heterogêneos** (json/text/xml) — a UI trata por
    `format` e só extrai métricas de JSON.
  - **Comparação é efêmera (não persistida).** `baselinePlan`/`baselineNote`
    **não** entram no `save/loadSession` (Fase 2b) nem no shape de `QuerySession`
    — são resetados na troca de conexão e no `loadSession`. Motivo: o baseline é
    um artefato de trabalho de uma iteração de otimização, não parte do estado
    salvo da sessão. **Para uma fase futura:** se quiser histórico de comparações,
    persistir `baseline_explain` em `query_sessions` (nova coluna + migration).
  - **"Índice avaliado" é rótulo, não execução.** O dropdown só documenta qual
    `suggested_index` o usuário aplicou manualmente antes do 2º EXPLAIN —
    coerente com §8.1 (índices são texto, nunca executados pelo módulo). Não há
    HypoPG/índice hipotético; o ganho é medido comparando dois EXPLAIN reais.
  - **Reuso do componente `Badge`** (variants `info`/`muted`/`warning`) e do
    padrão de abas já existente na coluna 3. Nenhuma dependência nova.
- **Descobertas / para as próximas fases:**
  - **Nomes reais (contrato de UI):** aba `rightTab === "compare"`; componente
    `PlanComparison({baseline, current, note, indexes, onNote, onClear})`;
    helpers `extractPlanMetrics(plan, format)` e `deltaPct(baseline, current)`
    exportados de `QueryStudioPage.tsx` (reusáveis por teste/Fase 3c).
  - **Fase 3c (anti-padrões):** `performanceNotes`/`assumptions` seguem
    renderizados como listas simples; para destacá-los com `Badge`/cores, o ponto
    é o bloco "Notas de performance" na coluna 3 (por volta de onde `suggestedIndexes`
    é renderizado). O padrão de cores verde/vermelho já usado em `PlanComparison`
    (delta) serve de referência.
  - **Fase 3d (config ANALYZE):** a UI ainda decide EXPLAIN só por dialeto
    (`EXPLAIN_KINDS`); quando o backend expuser a flag `QUERY_STUDIO_ALLOW_ANALYZE`,
    condicionar o botão "EXPLAIN ANALYZE" a essa capacidade (nada nesta fase mexeu
    nisso).
  - **Métricas extraíveis por dialeto (estado atual):** só `pg` e `mysql`
    (`format:"json"`) rendem tabela de variação; `mssql` (xml) e MySQL ANALYZE
    (text/TREE) caem no diff textual. Se a Fase 2c ganhar EXPLAIN JSON em mais
    dialetos, `extractPlanMetrics` só precisa de um novo ramo de parse.
  - `npm run build` no PowerShell conclui com `✓ built` (aviso de chunk >500 kB
    cosmético/pré-existente; `2>$null` evita o `NativeCommandError` do stderr do
    vite).
- **Testes:** `./node_modules/.bin/tsc --noEmit -p .` → OK (sem erros);
  `npm run build` → OK (`✓ built`); `go build ./...` → OK (backend intocado,
  rodado por garantia). `go test ./internal/api/...` não requerido (backend não
  mudou).
- **Commit:** (a cargo do orquestrador)

