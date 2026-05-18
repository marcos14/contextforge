import { useState } from "react";

type SectionId =
  | "intro"
  | "primeiros-passos"
  | "conexoes"
  | "grupos"
  | "tools"
  | "tokens"
  | "clientes-mcp"
  | "backup"
  | "lgpd"
  | "seguranca"
  | "boas-praticas"
  | "perguntas";

type Section = {
  id: SectionId;
  title: string;
  content: React.ReactNode;
};

export function HowToPage() {
  const [active, setActive] = useState<SectionId>("intro");

  const sections: Section[] = [
    {
      id: "intro",
      title: "Visão geral",
      content: (
        <>
          <P>
            O <B>ContextForge</B> é uma plataforma para expor fontes de dados (bancos
            SQL, MongoDB e APIs REST) como <I>tools</I> consumíveis por agentes de IA
            via o protocolo <B>MCP (Model Context Protocol)</B>.
          </P>
          <P>
            Em vez de dar acesso direto ao banco para um modelo, você define{" "}
            <I>queries parametrizadas</I> ou <I>scripts JavaScript</I>{" "}
            (<Code>kind=code</Code>) que o agente pode invocar. Cada tool tem entrada
            tipada, limite de linhas, timeout e cache opcional.
          </P>
          <Callout variant="info" title="Fluxo macro">
            <Ol>
              <li>Cadastre uma <B>Connection</B> (credenciais criptografadas no banco).</li>
              <li>Crie um <B>Group</B> e dentro dele uma ou mais <B>Tools</B>.</li>
              <li>Gere um <B>Token</B> e conceda permissões (grants) por tool ou grupo.</li>
              <li>Conecte um cliente MCP (Claude, Copilot, etc.) ao endpoint <Code>/mcp</Code>.</li>
            </Ol>
          </Callout>
        </>
      ),
    },
    {
      id: "primeiros-passos",
      title: "Primeiros passos",
      content: (
        <>
          <H3>1. Faça login</H3>
          <P>
            Use a credencial de bootstrap configurada (variáveis{" "}
            <Code>BOOTSTRAP_ADMIN_EMAIL</Code> e <Code>BOOTSTRAP_ADMIN_PASSWORD</Code>)
            ou peça a um administrador para criá-la em <I>Administração → Usuários</I>.
          </P>
          <H3>2. Troque a senha inicial</H3>
          <P>
            Imediatamente após o primeiro login, abra <I>Administração → Usuários</I>{" "}
            e altere a senha do admin bootstrap.
          </P>
          <H3>3. Verifique o ambiente</H3>
          <P>
            Em <I>Administração → Configurações</I> confira se Postgres, Redis e LLM
            (se configurado) estão online. Anote o <I>fingerprint</I> da{" "}
            <Code>MASTER_KEY</Code> — você vai precisar dele para restaurar backups.
          </P>
        </>
      ),
    },
    {
      id: "conexoes",
      title: "Conexões (Connections)",
      content: (
        <>
          <P>
            Uma <B>Connection</B> guarda credenciais de uma fonte de dados. Tipos
            suportados: <Code>pg</Code>, <Code>mysql</Code>, <Code>mssql</Code>,{" "}
            <Code>oracle</Code>, <Code>mongo</Code>, <Code>firebird</Code> e{" "}
            <Code>rest</Code>.
          </P>
          <Callout variant="warning" title="Princípio do menor privilégio">
            <Ul>
              <li>
                Crie um <B>usuário de banco específico</B> para o ContextForge, com
                permissões mínimas — idealmente <Code>SELECT</Code> apenas nas tabelas/views
                que serão expostas.
              </li>
              <li>
                Para APIs REST, use uma <B>API key</B> com escopo restrito; nunca uma
                credencial mestre.
              </li>
              <li>
                Considere conectar a uma <B>réplica de leitura</B> ou a um schema/dataset
                separado contendo apenas dados desidentificados.
              </li>
            </Ul>
          </Callout>
          
        </>
      ),
    },
    {
      id: "grupos",
      title: "Grupos",
      content: (
        <>
          <P>
            <B>Groups</B> organizam tools relacionadas e simplificam a gestão de
            permissões — você pode conceder acesso a um grupo inteiro em vez de
            tool por tool.
          </P>
          <P>
            Marque o grupo como <B>hidden by default</B> quando ele contém tools
            sensíveis: o token só verá tools desse grupo se receber um{" "}
            <I>grant</I> explícito.
          </P>
        </>
      ),
    },
    {
      id: "tools",
      title: "Tools",
      content: (
        <>
          <P>
            Uma <B>Tool</B> é uma operação parametrizada exposta para o agente de
            IA. Há dois tipos:
          </P>
          <Ul>
            <li>
              <B>Query</B> — uma instrução SQL/Mongo/REST executada na conexão
              associada, com parâmetros tipados.
            </li>
            <li>
              <B>Code</B> — um script JavaScript executado num sandbox isolado
              (goja), com timeout e limites; pode chamar outras tools.
            </li>
          </Ul>
          <Callout variant="info" title="Assistente de IA integrado">
            <P>
              Dentro da página <I>Tools</I>, ao criar ou editar uma tool, há um{" "}
              <B>chat lateral com um agente de IA</B> que ajuda a montar a query, o{" "}
              <Code>params_schema</Code> e a descrição. Ele recebe:
            </P>
            <Ul>
              <li>
                Para tools <B>query</B>: o tipo de conexão e o esquema das tabelas
                selecionadas (introspecção automática) — basta descrever em
                português o que você precisa.
              </li>
              <li>
                Para tools <B>code</B>: a lista de conexões e tools ativas
                disponíveis no sandbox, para que o snippet gerado já chame
                <Code>tools.call(...)</Code> e <Code>db.query(...)</Code> corretamente.
              </li>
              <li>
                Após um <I>preview</I> bem-sucedido, o botão <B>"Documentar com IA"</B>{" "}
                preenche título e descrição com base no resultado real — útil para
                que o modelo cliente escolha a tool certa em tempo de execução.
              </li>
            </Ul>
            <P>
              O assistente <B>só está disponível</B> quando a variável{" "}
              <Code>OPENROUTER_API_KEY</Code> está configurada no servidor. Veja{" "}
              <I>Administração → Configurações</I> para confirmar.
            </P>
            <P>
              <B>Atenção:</B> o conteúdo enviado ao agente (esquema, prompts e
              resultados de preview) é transmitido para o provedor de LLM. Não
              cole dados pessoais reais nos prompts — use dados sintéticos ou
              mascarados durante a construção da tool.
            </P>
          </Callout>
          <Callout variant="warning" title="Cuidados ao criar uma tool">
            <Ul>
              <li>
                <B>Sempre use placeholders</B> para parâmetros (<Code>$1</Code>,{" "}
                <Code>:name</Code> etc.). Nunca interpole strings vindas do agente
                diretamente na query.
              </li>
              <li>
                Defina <Code>row_limit</Code> compatível com o caso de uso. Resultados
                grandes consomem contexto do modelo e podem expor mais dados do que o
                necessário.
              </li>
              <li>
                Ajuste o <Code>timeout_ms</Code> para evitar queries longas que travam
                o cliente.
              </li>
              <li>
                Use <Code>cache_ttl_sec</Code> em consultas de leitura — reduz carga no
                banco e custo do agente.
              </li>
              <li>
                Marque a tool como <B>draft</B> enquanto valida; só publique como{" "}
                <B>active</B> depois de testar com dados reais.
              </li>
            </Ul>
          </Callout>
          <P>
            Toda alteração de tool gera uma nova versão em{" "}
            <Code>tool_versions</Code>, permitindo auditoria e rollback.
          </P>
        </>
      ),
    },
    {
      id: "tokens",
      title: "Tokens MCP",
      content: (
        <>
          <P>
            Um <B>Token</B> é a credencial que clientes MCP usam para se autenticar.
            Cada token tem:
          </P>
          <Ul>
            <li>Um <Code>rate_limit_per_min</Code> (limite global de chamadas).</li>
            <li>
              Uma <Code>ip_allowlist</Code> opcional (lista de CIDRs/IPs permitidos).
            </li>
            <li>
              Um conjunto de <B>grants</B> — autorização explícita a tools ou grupos.
            </li>
          </Ul>
          <Callout variant="danger" title="O segredo só aparece uma vez">
            <P>
              Ao criar um token, o segredo em texto claro <B>é exibido apenas no
              momento da criação</B>. Depois disso, o backend mantém só o hash
              SHA-256. Guarde o segredo num cofre (1Password, Vault, etc.) — se
              perdido, o token precisa ser revogado e recriado.
            </P>
          </Callout>
          <Callout variant="warning" title="Princípio do menor privilégio (tokens)">
            <Ul>
              <li>
                Conceda <B>apenas as tools necessárias</B> para cada cliente. Evite
                grants em grupos inteiros se só uma tool for usada.
              </li>
              <li>
                Use uma <B>IP allowlist</B> sempre que o cliente for fixo (servidor
                conhecido, VPN corporativa).
              </li>
              <li>
                Revogue tokens não utilizados há muito tempo ou quando o usuário
                responsável sair da equipe.
              </li>
            </Ul>
          </Callout>
        </>
      ),
    },
    {
      id: "clientes-mcp",
      title: "Conectando clientes MCP",
      content: (
        <>
          <P>
            Use a página <I>Connect clients</I> para gerar o snippet de configuração
            do cliente (Claude Desktop, Cursor, etc.). O endpoint exposto é{" "}
            <Code>/mcp</Code> sob a URL pública configurada em{" "}
            <Code>PUBLIC_BASE_URL</Code>.
          </P>
          <P>
            O cliente envia o token no header <Code>Authorization: Bearer ...</Code>{" "}
            e o backend valida prefixo + hash em tempo constante antes de aplicar
            grants e rate-limit.
          </P>
        </>
      ),
    },
    {
      id: "backup",
      title: "Backup e restauração",
      content: (
        <>
          <P>
            A página <I>Backup</I> permite exportar conexões, grupos e tools (com
            versões) num arquivo <Code>.mcpbak</Code> criptografado com a{" "}
            <Code>MASTER_KEY</Code> do servidor.
          </P>
          <Callout variant="info" title="Portabilidade">
            <P>
              Backups <B>só podem ser restaurados em instâncias com a mesma{" "}
              <Code>MASTER_KEY</Code></B>. Verifique o <I>fingerprint</I> em{" "}
              <I>Administração → Configurações</I> para confirmar.
            </P>
          </Callout>
          <Callout variant="warning" title="Não incluso no backup">
            <Ul>
              <li>
                <B>Usuários administrativos</B> e suas senhas.
              </li>
              <li>
                <B>Tokens MCP</B> (são segredos opacos — recrie-os se mover de
                ambiente).
              </li>
              <li><B>Logs de auditoria</B>.</li>
            </Ul>
          </Callout>
          <P>
            Na restauração, escolha por item entre <B>Pular</B>, <B>Sobrescrever</B>{" "}
            ou <B>Duplicar</B>. Tudo é aplicado numa única transação — em caso de
            erro, nada é alterado.
          </P>
        </>
      ),
    },
    {
      id: "lgpd",
      title: "LGPD — pontos críticos",
      content: (
        <>
          <P>
            Quando o ContextForge expõe dados pessoais a um modelo de IA, sua
            organização atua como <B>controlador</B> (ou operador) sob a LGPD
            (Lei 13.709/2018). Trate cada tool como um <I>tratamento de dados</I> e
            siga estes princípios:
          </P>

          <H3>1. Finalidade e necessidade</H3>
          <P>
            Cada tool deve atender a uma <B>finalidade legítima, específica e
            informada</B> (art. 6º, I e III). Documente no campo{" "}
            <Code>description</Code> qual problema ela resolve e <B>quem pode
            usá-la</B>.
          </P>

          <H3>2. Minimização de dados</H3>
          <Ul>
            <li>
              Selecione apenas as colunas necessárias — <Code>SELECT *</Code> é{" "}
              <B>quase sempre errado</B>.
            </li>
            <li>
              Aplique <Code>row_limit</Code> agressivo; agregue/sumarize quando
              possível.
            </li>
            <li>
              Use <B>views ou functions no banco</B> que já entreguem o dado
              desidentificado ou agregado.
            </li>
          </Ul>

          <H3>3. Pseudonimização e mascaramento</H3>
          <P>
            Quando o agente <B>não precisa</B> do dado pessoal real (CPF, e-mail,
            endereço, telefone), faça mascaramento na query:
          </P>
          <Pre>
{`-- Exemplo: máscara de CPF e e-mail
SELECT
  id,
  regexp_replace(cpf, '(\\d{3})\\d{6}(\\d{2})', '\\1******\\2') AS cpf,
  regexp_replace(email, '(^.).*(@.*$)', '\\1***\\2') AS email
FROM clientes`}
          </Pre>

          <H3>4. Base legal</H3>
          <P>
            Antes de criar uma tool sobre dados pessoais, confirme a{" "}
            <B>base legal</B> aplicável (consentimento, execução de contrato,
            obrigação legal, legítimo interesse etc.). Se a base for{" "}
            <I>legítimo interesse</I>, documente o teste de balanceamento (LIA).
          </P>

          <H3>5. Dados sensíveis</H3>
          <Callout variant="danger" title="Atenção redobrada">
            <P>
              Dados de <B>saúde, biométricos, raça/etnia, orientação sexual,
              convicção religiosa/política, filiação sindical e dados de
              crianças/adolescentes</B> têm proteção reforçada (arts. 11 e 14).
              Evite expô-los a modelos de IA salvo se houver base legal específica
              e revisão jurídica.
            </P>
          </Callout>

          <H3>6. Transferência internacional</H3>
          <P>
            Se a LLM configurada está hospedada fora do Brasil (OpenAI, Anthropic,
            OpenRouter, etc.), o envio de dados pessoais constitui{" "}
            <B>transferência internacional</B> (arts. 33–36). Garanta cláusulas
            contratuais adequadas e informe os titulares na política de privacidade.
          </P>

          <H3>7. Direitos do titular</H3>
          <P>
            A LGPD garante aos titulares (art. 18) os direitos de acesso, correção,
            anonimização, portabilidade e eliminação. Use a tabela{" "}
            <Code>audit_logs</Code> e <Code>tool_executions</Code> para responder a
            requisições de acesso ou para excluir registros relacionados.
          </P>

          <H3>8. Logging consciente</H3>
          <Ul>
            <li>
              <Code>tool_executions</Code> grava <Code>params_redacted</Code>{" "}
              (parâmetros redatados) — revise o que está sendo logado em tools
              sensíveis.
            </li>
            <li>
              Não inclua dados pessoais em <Code>description</Code>,{" "}
              <Code>query_text</Code> ou exemplos.
            </li>
            <li>Defina retenção de logs compatível com a finalidade.</li>
          </Ul>

          <H3>9. Incidentes</H3>
          <P>
            Em caso de vazamento (ex.: token comprometido com acesso a dados
            pessoais), comunique a ANPD e os titulares afetados em prazo razoável
            (art. 48). Revogue o token, gere novas credenciais e investigue via{" "}
            <I>Auditoria</I> e <Code>tool_executions</Code>.
          </P>
        </>
      ),
    },
    {
      id: "seguranca",
      title: "Segurança",
      content: (
        <>
          <H3>Segredos da plataforma</H3>
          <Ul>
            <li>
              <Code>MASTER_KEY</Code> (32 bytes, base64) cifra todas as credenciais
              de conexão. <B>Faça backup seguro</B> dela — sem ela, dados criptografados
              ficam irrecuperáveis.
            </li>
            <li>
              <Code>JWT_SECRET</Code> assina tokens de sessão da UI. Use ≥ 32 chars
              aleatórios.
            </li>
            <li>
              Armazene segredos num <B>cofre</B> (Vault, AWS Secrets Manager,
              GitHub Actions secrets). Nunca commite <Code>.env</Code>.
            </li>
          </Ul>

          <H3>Defesa em profundidade</H3>
          <Ul>
            <li>
              <B>SQL safety</B>: o backend valida queries em <Code>drivers/safety.go</Code>
              {" "}— não relaxe essas verificações.
            </li>
            <li>
              <B>Sandbox JS</B>: tools <Code>kind=code</Code> rodam em goja com timeout
              e limites de memória. Mantenha-os.
            </li>
            <li>
              <B>Rate limit</B>: ajuste <Code>rate_limit_per_min</Code> por token de
              acordo com o perfil esperado. Picos anormais indicam abuso.
            </li>
            <li>
              <B>CORS</B>: configure <Code>CORS_ALLOWED_ORIGINS</Code> com a origem
              exata do frontend admin — nunca <Code>*</Code> em produção.
            </li>
            <li>
              <B>HTTPS obrigatório</B> em produção, com certificado válido. Tokens
              MCP trafegam no header <Code>Authorization</Code>.
            </li>
          </Ul>

          <H3>Higiene operacional</H3>
          <Ul>
            <li>Revise <I>Auditoria</I> semanalmente.</li>
            <li>
              Aplique a <B>regra mínima</B> em papéis: viewer onde for possível,
              editor para quem edita tools, admin só para responsáveis pela
              plataforma.
            </li>
            <li>
              Mantenha a stack atualizada (Go, libs, Postgres, Redis). Vulnerabilidades
              em drivers podem comprometer a segurança da camada SQL.
            </li>
            <li>
              Faça <B>backups regulares</B> e teste a restauração num ambiente
              separado.
            </li>
          </Ul>

          <Callout variant="danger" title="Sinais de comprometimento">
            <Ul>
              <li>Picos súbitos de execuções com erro ou negação.</li>
              <li>
                Uso de token a partir de IP fora do esperado (cruze com{" "}
                <Code>tool_executions.client_ip</Code>).
              </li>
              <li>Mudanças não atribuídas em tools/conexões (veja Auditoria).</li>
              <li>Falhas de autenticação em massa.</li>
            </Ul>
            <P>
              Em qualquer um desses sinais: revogue o(s) token(s) suspeito(s),
              rote a <Code>MASTER_KEY</Code> (re-cifrando conexões) e abra um
              incidente.
            </P>
          </Callout>
        </>
      ),
    },
    {
      id: "boas-praticas",
      title: "Boas práticas",
      content: (
        <>
          <Ul>
            <li>
              <B>Nomeie tools com clareza</B> — slugs como <Code>buscar_cliente_por_id</Code>{" "}
              vencem <Code>q1</Code>.
            </li>
            <li>
              Escreva <B>descriptions ricas</B>: o modelo escolhe a tool com base nisso.
              Inclua quando NÃO usar.
            </li>
            <li>
              Defina <B>params_schema</B> com tipos, <Code>enum</Code> e{" "}
              <Code>description</Code> em cada parâmetro.
            </li>
            <li>
              Adicione um <B>exemplo de uso</B> na description quando o input não for
              óbvio.
            </li>
            <li>
              Para queries que mudam pouco, ative <B>cache</B>; para dados sensíveis,
              prefira <Code>cache_per_token</Code> ou desabilite o cache.
            </li>
            <li>
              Mantenha <B>conexões somente-leitura</B> sempre que possível; tools
              de escrita exigem revisão extra.
            </li>
            <li>
              Versione mudanças importantes via <B>backup</B> antes de grandes
              alterações.
            </li>
          </Ul>
        </>
      ),
    },
    {
      id: "perguntas",
      title: "Perguntas frequentes",
      content: (
        <>
          <H3>Esqueci o segredo de um token. O que faço?</H3>
          <P>
            Não há como recuperar — revogue o token e crie um novo. Atualize o
            cliente MCP com a nova credencial.
          </P>

          <H3>Posso usar o ContextForge com dados pessoais?</H3>
          <P>
            Sim, desde que respeite a LGPD: base legal definida, minimização,
            mascaramento quando aplicável, retenção controlada e revisão da
            transferência internacional para o provedor de LLM.
          </P>

          <H3>Como rotacionar a <Code>MASTER_KEY</Code>?</H3>
          <P>
            Faça um backup, reconfigure a nova chave, recrie as conexões a partir
            do backup (que será recifrado com a nova chave) e descarte o backup
            antigo. Coordene janela de manutenção — durante a troca o serviço fica
            indisponível.
          </P>

          <H3>Quem tem acesso ao quê?</H3>
          <Ul>
            <li><B>admin</B> — gerencia usuários, backup, tudo.</li>
            <li><B>editor</B> — CRUD de conexões, grupos, tools, tokens, grants e backup.</li>
            <li><B>viewer</B> — leitura: <Code>/me</Code>, registry, execuções.</li>
          </Ul>
        </>
      ),
    },
  ];

  const current = sections.find((s) => s.id === active) ?? sections[0];

  return (
    <div className="max-w-6xl">
      <div className="mb-4">
        <h1 className="text-2xl font-bold">Como usar o ContextForge</h1>
        <p className="text-sm text-slate-500">
          Guia rápido, cuidados operacionais, LGPD e segurança.
        </p>
      </div>

      <div className="grid gap-4 md:grid-cols-[14rem_1fr]">
        <nav className="md:sticky md:top-4 self-start border border-border rounded bg-white">
          <ul className="text-sm">
            {sections.map((s) => (
              <li key={s.id}>
                <button
                  onClick={() => setActive(s.id)}
                  className={
                    "w-full text-left px-3 py-2 border-l-2 " +
                    (s.id === active
                      ? "border-primary bg-muted font-medium"
                      : "border-transparent hover:bg-muted")
                  }
                >
                  {s.title}
                </button>
              </li>
            ))}
          </ul>
        </nav>

        <article className="border border-border rounded bg-white p-5 md:p-6 prose-sm max-w-none">
          <h2 className="text-xl font-bold mb-3">{current.title}</h2>
          <div className="space-y-3 text-sm leading-relaxed text-slate-800">
            {current.content}
          </div>
        </article>
      </div>
    </div>
  );
}

// ============ tiny presentational helpers ============

function P({ children }: { children: React.ReactNode }) {
  return <p>{children}</p>;
}

function H3({ children }: { children: React.ReactNode }) {
  return <h3 className="font-semibold text-slate-900 mt-3">{children}</h3>;
}

function B({ children }: { children: React.ReactNode }) {
  return <strong className="font-semibold text-slate-900">{children}</strong>;
}

function I({ children }: { children: React.ReactNode }) {
  return <em>{children}</em>;
}

function Code({ children }: { children: React.ReactNode }) {
  return (
    <code className="bg-muted px-1 py-0.5 rounded text-[0.85em] font-mono">
      {children}
    </code>
  );
}

function Pre({ children }: { children: React.ReactNode }) {
  return (
    <pre className="bg-slate-900 text-slate-100 text-xs rounded p-3 overflow-x-auto">
      {children}
    </pre>
  );
}

function Ul({ children }: { children: React.ReactNode }) {
  return <ul className="list-disc pl-5 space-y-1">{children}</ul>;
}

function Ol({ children }: { children: React.ReactNode }) {
  return <ol className="list-decimal pl-5 space-y-1">{children}</ol>;
}

function Callout({
  title,
  children,
  variant = "info",
}: {
  title: string;
  children: React.ReactNode;
  variant?: "info" | "warning" | "danger";
}) {
  const styles = {
    info: "border-blue-200 bg-blue-50 text-blue-900",
    warning: "border-amber-200 bg-amber-50 text-amber-900",
    danger: "border-red-200 bg-red-50 text-red-900",
  }[variant];
  return (
    <div className={`border rounded p-3 my-2 ${styles}`}>
      <div className="font-semibold text-sm mb-1">{title}</div>
      <div className="space-y-2 text-sm">{children}</div>
    </div>
  );
}
