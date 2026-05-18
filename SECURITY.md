# Politica de Seguranca

## Versoes suportadas

Enquanto o projeto estiver em fase inicial, a branch `main` recebe correcoes de seguranca. Releases versionadas poderao ter uma matriz de suporte propria no futuro.

## Como reportar uma vulnerabilidade

Nao abra uma issue publica para vulnerabilidades.

Envie um reporte privado pelo GitHub Security Advisories do repositorio:

https://github.com/marcos14/contextforge/security/advisories/new

Inclua, quando possivel:

- Descricao clara do problema.
- Passos de reproducao ou prova de conceito.
- Impacto esperado.
- Versao, commit ou ambiente afetado.
- Logs ou mensagens de erro sem segredos.

## Escopo

Temas de interesse especial neste projeto:

- Exposicao indevida de tokens MCP, senhas, `MASTER_KEY`, `JWT_SECRET` ou chaves de provedores LLM.
- Bypass de autenticacao/autorizacao na API administrativa ou no endpoint `/mcp`.
- SQL injection, execucao destrutiva de queries ou bypass das protecoes de `drivers/safety.go`.
- Vazamento de credenciais de conexao armazenadas.
- Escape do sandbox de tools tipo `code`.
- Falhas de criptografia em backup/restore ou conexoes.

## Boas praticas ao operar

- Nunca publique `.env`, `.mcpbak`, tokens MCP ou credenciais de banco.
- Use usuarios read-only nas conexoes de banco sempre que possivel.
- Configure HTTPS em producao.
- Rotacione `MASTER_KEY`, `JWT_SECRET`, tokens MCP e chaves LLM em caso de suspeita de vazamento.

## SLA

Este projeto e mantido em melhor esforco. Reportes com impacto pratico e reproducao clara terao prioridade.
