import { Link, useNavigate } from "react-router-dom";
import { useEffect } from "react";
import { getAuth } from "../auth";

export function LandingPage() {
  const navigate = useNavigate();
  const auth = getAuth();

  useEffect(() => {
    if (auth) navigate("/dashboard", { replace: true });
  }, [auth, navigate]);

  if (auth) return null;

  return (
    <div className="min-h-screen flex flex-col bg-slate-50">
      {/* Header */}
      <header className="bg-white border-b border-border px-6 py-4 flex items-center justify-between">
        <span className="font-bold text-xl text-slate-800">ContextForge</span>
        <nav className="flex items-center gap-4">
          <Link to="/how-to" className="text-sm text-slate-600 hover:text-primary transition-colors">
            Como usar
          </Link>
          <Link to="/connect" className="text-sm text-slate-600 hover:text-primary transition-colors">
            Conectar
          </Link>
          <a
            href="https://github.com/marcos14/contextforge"
            target="_blank"
            rel="noopener noreferrer"
            className="text-sm text-slate-600 hover:text-primary transition-colors flex items-center gap-1"
          >
            <GitHubIcon />
            <span className="hidden sm:inline">GitHub</span>
          </a>
          <Link
            to="/login"
            className="text-sm bg-primary text-white px-4 py-2 rounded hover:opacity-90 transition-opacity"
          >
            Entrar
          </Link>
        </nav>
      </header>

      {/* Hero */}
      <section className="flex-1 flex flex-col items-center justify-center px-6 py-20 text-center">
        <div className="mb-4 w-16 h-16 rounded-2xl bg-primary flex items-center justify-center shadow-lg">
          <MCPIcon />
        </div>
        <h1 className="text-4xl sm:text-5xl font-bold text-slate-800 mb-4 leading-tight">
          ContextForge
        </h1>
        <p className="text-lg text-slate-500 max-w-xl mb-8">
          Exponha suas fontes de dados — SQL, REST, MongoDB e mais — como ferramentas MCP para
          que seus agentes de IA as utilizem com segurança via tokens.
        </p>
        <div className="flex flex-wrap gap-3 justify-center mb-16">
          <Link
            to="/login"
            className="bg-primary text-white px-6 py-3 rounded-lg font-medium hover:opacity-90 transition-opacity"
          >
            Entrar no sistema
          </Link>
          <Link
            to="/how-to"
            className="bg-white text-slate-700 border border-border px-6 py-3 rounded-lg font-medium hover:bg-muted transition-colors"
          >
            Como usar
          </Link>
        </div>

        {/* Features */}
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-6 max-w-3xl w-full text-left">
          <FeatureCard
            icon={<IconDB />}
            title="Múltiplas fontes"
            description="Conecte bancos PostgreSQL, MySQL, SQL Server, MongoDB, Oracle, REST APIs e mais."
          />
          <FeatureCard
            icon={<IconKey />}
            title="Controle de acesso"
            description="Tokens com escopos e grants definem exatamente o que cada cliente MCP pode fazer."
          />
          <FeatureCard
            icon={<IconShield />}
            title="Segurança integrada"
            description="Credenciais criptografadas com AES-256-GCM. Nenhum segredo em texto claro nos logs."
          />
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t border-border bg-white px-6 py-5 flex flex-col sm:flex-row items-center justify-between gap-3 text-sm text-slate-500">
        <span>ContextForge &copy; {new Date().getFullYear()}</span>
        <div className="flex items-center gap-5">
          <Link to="/how-to" className="hover:text-primary transition-colors">
            Como usar
          </Link>
          <Link to="/connect" className="hover:text-primary transition-colors">
            Conectar clientes MCP
          </Link>
          <a
            href="https://github.com/marcos14/contextforge"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 hover:text-primary transition-colors"
          >
            <GitHubIcon />
            Sugestões e contato
          </a>
        </div>
      </footer>
    </div>
  );
}

// ---- Feature card ----------------------------------------------------------------

function FeatureCard({
  icon,
  title,
  description,
}: {
  icon: React.ReactNode;
  title: string;
  description: string;
}) {
  return (
    <div className="bg-white border border-border rounded-xl p-5 shadow-sm">
      <div className="mb-3 w-9 h-9 rounded-lg bg-primary/10 flex items-center justify-center text-primary">
        {icon}
      </div>
      <h3 className="font-semibold text-slate-800 mb-1">{title}</h3>
      <p className="text-sm text-slate-500 leading-relaxed">{description}</p>
    </div>
  );
}

// ---- Inline icons ----------------------------------------------------------------

function GitHubIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
      <path d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0 1 12 6.844a9.59 9.59 0 0 1 2.504.337c1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.02 10.02 0 0 0 22 12.017C22 6.484 17.522 2 12 2z" />
    </svg>
  );
}

function MCPIcon() {
  return (
    <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M12 2L2 7l10 5 10-5-10-5z" />
      <path d="M2 17l10 5 10-5" />
      <path d="M2 12l10 5 10-5" />
    </svg>
  );
}

function IconDB() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <ellipse cx="12" cy="5" rx="9" ry="3" />
      <path d="M3 5v6c0 1.66 4.03 3 9 3s9-1.34 9-3V5" />
      <path d="M3 11v6c0 1.66 4.03 3 9 3s9-1.34 9-3v-6" />
    </svg>
  );
}

function IconKey() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <circle cx="8" cy="14" r="4" />
      <path d="M11 11l9-9" />
      <path d="M16 7l3 3" />
      <path d="M19 4l2 2" />
    </svg>
  );
}

function IconShield() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M12 2l8 4v6c0 5-8 10-8 10S4 17 4 12V6l8-4z" />
      <path d="M9 12l2 2 4-4" />
    </svg>
  );
}
