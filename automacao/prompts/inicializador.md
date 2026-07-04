Você vai preparar um plano para o **autopilot** — um orquestrador que executa fases de desenvolvimento uma a uma, cada fase numa execução independente do Claude (contexto limpo), com testes como gate e um commit por fase.

O plano do usuário está em `{PLANO}`. Leia-o por inteiro. Leia também as instruções do projeto (CLAUDE.md/AGENTS.md/README, se existirem) para entender a stack e os comandos de verificação.

Sua tarefa tem 3 partes:

**1. Quebre o plano em micro-fases**, cada uma executável CONFORTAVELMENTE em UMA única execução do Claude (modelo Opus). Critérios de tamanho:
- Uma fatia vertical de valor: algo testável/demonstrável ao final.
- Escopo de poucas horas de trabalho (na ordem de meia dúzia de arquivos novos/alterados) — não uma reescrita de subsistema.
- Tem os PRÓPRIOS testes (a fase só conta como pronta com testes verdes).
- Declara dependências explícitas de outras fases (apenas as reais, para permitir paralelismo futuro).
- Fases que exigem hardware físico, aprovação externa ou decisão de negócio devem ser marcadas com `requer_humano` e o motivo em `observacao`.
- Se uma fase do plano original for grande demais, divida-a (ex.: 4a, 4b...). Se já estiver do tamanho certo, mantenha como está. Fases já concluídas no plano recebem `status=concluida`.

**2. Edite `{PLANO}`** para refletir as micro-fases: cada fase com meta, checklist de tarefas (checkboxes `- [ ]`), linha "Depende de:" e critérios de teste. PRESERVE o conteúdo e o histórico existentes (decisões, registros) — reorganize, não apague. Se o plano ainda não tiver uma seção **Registro de Andamento**, crie-a (é a memória compartilhada entre as fases: cada execução adiciona uma entrada com o que fez e o que descobriu).

**3. Identifique os gates de verificação** do(s) projeto(s): os comandos determinísticos que provam que o código está saudável (build, análise estática, checagem de formatação, testes). Use os comandos que o próprio projeto já documenta. Para repositórios adicionais em outro repo, marque `somente_se_mudou=true`. Se houver verificações mais lentas ou que exigem setup especial (ex.: testes de integração), modele-as como um gate extra separado e referencie esse gate na coluna `gate_extra` apenas das fases que precisam dele.

Sua resposta final deve ser apenas o JSON estruturado solicitado (`fases`, `gates` e, se aplicável, `gates_extra`).
