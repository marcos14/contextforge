export function ConnectClientsPage() {
  const base = window.location.origin;
  const url = `${base}/mcp`;
  return (
    <div className="space-y-6 max-w-3xl">
      <h2 className="text-2xl font-bold">Conectar seu cliente MCP</h2>
      <p className="text-sm text-gray-600">
        Use o token de cliente (gerado em <b>Tokens</b>) como Bearer no header
        <code className="bg-muted px-1">Authorization</code>. O endpoint do servidor é:
      </p>
      <pre className="bg-muted p-3 rounded text-sm font-mono">{url}</pre>

      <Section title="ChatGPT (Custom Connectors)">
        <p>Em <i>Settings → Beta → Connectors</i>, adicione um custom connector remoto apontando para a URL acima e cole seu Bearer token.</p>
      </Section>

      <Section title="Claude.ai (web · Integrations)">
        <p>Em <i>Settings → Integrations → Add custom integration</i>, informe a URL e o Bearer token.</p>
      </Section>

      <Section title="Claude Desktop (via bridge)">
        <p>Claude Desktop suporta apenas stdio. Use o bridge <code>mcp-remote</code>:</p>
        <pre className="bg-muted p-3 rounded text-sm font-mono whitespace-pre-wrap">{`{
  "mcpServers": {
    "contextforge": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "${url}", "--header", "Authorization: Bearer SEU_TOKEN"]
    }
  }
}`}</pre>
      </Section>

      <Section title="Cursor">
        <p>Em <code>~/.cursor/mcp.json</code>:</p>
        <pre className="bg-muted p-3 rounded text-sm font-mono whitespace-pre-wrap">{`{
  "mcpServers": {
    "contextforge": {
      "url": "${url}",
      "headers": { "Authorization": "Bearer SEU_TOKEN" }
    }
  }
}`}</pre>
      </Section>

      <Section title="VS Code (GitHub Copilot Chat)">
        <p>No workspace, crie <code>.vscode/mcp.json</code>:</p>
        <pre className="bg-muted p-3 rounded text-sm font-mono whitespace-pre-wrap">{`{
  "servers": {
    "contextforge": {
      "type": "http",
      "url": "${url}",
      "headers": { "Authorization": "Bearer SEU_TOKEN" }
    }
  }
}`}</pre>
      </Section>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="font-semibold">{title}</h3>
      <div className="text-sm text-gray-700 space-y-1">{children}</div>
    </section>
  );
}
