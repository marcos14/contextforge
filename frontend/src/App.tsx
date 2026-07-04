import { Outlet, NavLink, useNavigate } from "react-router-dom";
import { useState, ReactNode } from "react";
import { clearAuth, getAuth } from "./auth";

// ---- Icons (inline, no deps) -------------------------------------------------
// Stroke uses currentColor so icons inherit the link's text color.

type IconProps = { className?: string };
const I =
  (paths: ReactNode) =>
  ({ className }: IconProps) => (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden
    >
      {paths}
    </svg>
  );

const IconDashboard = I(
  <>
    <rect x="3" y="3" width="7" height="9" rx="1.5" />
    <rect x="14" y="3" width="7" height="5" rx="1.5" />
    <rect x="14" y="12" width="7" height="9" rx="1.5" />
    <rect x="3" y="16" width="7" height="5" rx="1.5" />
  </>,
);
const IconConnections = I(
  <>
    <ellipse cx="12" cy="5" rx="8" ry="3" />
    <path d="M4 5v6c0 1.66 3.58 3 8 3s8-1.34 8-3V5" />
    <path d="M4 11v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6" />
  </>,
);
const IconGroups = I(
  <path d="M3 7h6l2 2h10v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z" />,
);
const IconTools = I(
  <path d="M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18l3 3 6.3-6.3a4 4 0 0 0 5.4-5.4l-2.5 2.5-2.4-.6-.6-2.4 2.5-2.5z" />,
);
const IconQueryStudio = I(
  <>
    <ellipse cx="12" cy="5" rx="8" ry="3" />
    <path d="M4 5v6c0 1.66 3.58 3 8 3s8-1.34 8-3V5" />
    <path d="M4 11v5c0 1.66 3.58 3 8 3" />
    <circle cx="17.5" cy="17.5" r="3" />
    <path d="M22 22l-2.1-2.1" />
  </>,
);
const IconTokens = I(
  <>
    <circle cx="8" cy="14" r="4" />
    <path d="M11 11l9-9" />
    <path d="M16 7l3 3" />
    <path d="M19 4l2 2" />
  </>,
);
const IconConnect = I(
  <>
    <rect x="2" y="4" width="20" height="14" rx="2" />
    <path d="M8 21h8" />
    <path d="M12 18v3" />
    <path d="M7 9l3 3-3 3" />
    <path d="M14 15h4" />
  </>,
);
const IconBackup = I(
  <>
    <path d="M21 12a9 9 0 1 1-3-6.7" />
    <path d="M21 3v6h-6" />
  </>,
);
const IconHowTo = I(
  <>
    <path d="M4 4h12a4 4 0 0 1 4 4v12H8a4 4 0 0 1-4-4V4z" />
    <path d="M4 16a4 4 0 0 1 4-4h12" />
    <path d="M8 8h6" />
    <path d="M8 11h4" />
  </>,
);
const IconUsers = I(
  <>
    <circle cx="9" cy="8" r="3.5" />
    <path d="M2.5 20c0-3.6 2.9-6 6.5-6s6.5 2.4 6.5 6" />
    <circle cx="17" cy="9" r="2.5" />
    <path d="M15 14.5c3 0 6 1.6 6 5" />
  </>,
);
const IconAudit = I(
  <>
    <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5z" />
    <path d="M14 3v5h5" />
    <path d="M8 13h8" />
    <path d="M8 17h6" />
  </>,
);
const IconSettings = I(
  <>
    <circle cx="12" cy="12" r="3" />
    <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 0 1-4 0v-.1a1.7 1.7 0 0 0-1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 0 1 0-4h.1a1.7 1.7 0 0 0 1.5-1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3h.1a1.7 1.7 0 0 0 1-1.5V3a2 2 0 0 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8v.1a1.7 1.7 0 0 0 1.5 1H21a2 2 0 0 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" />
  </>,
);
const IconLogout = I(
  <>
    <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
    <path d="M16 17l5-5-5-5" />
    <path d="M21 12H9" />
  </>,
);

// ---- Navigation model --------------------------------------------------------

type NavItem = {
  to: string;
  label: string;
  Icon: (p: IconProps) => JSX.Element;
};
type NavGroup = {
  id: string;
  label?: string; // omit to render flush (no header)
  items: NavItem[];
};

const groups: NavGroup[] = [
  {
    id: "overview",
    items: [{ to: "/dashboard", label: "Dashboard", Icon: IconDashboard }],
  },
  {
    id: "catalog",
    label: "Catálogo",
    items: [
      { to: "/connections", label: "Conexões", Icon: IconConnections },
      { to: "/groups", label: "Grupos", Icon: IconGroups },
      { to: "/tools", label: "Tools", Icon: IconTools },
      { to: "/query-studio", label: "Query Studio", Icon: IconQueryStudio },
    ],
  },
  {
    id: "access",
    label: "Acesso",
    items: [
      { to: "/tokens", label: "Tokens", Icon: IconTokens },
      { to: "/connect", label: "Clientes MCP", Icon: IconConnect },
    ],
  },
  {
    id: "ops",
    label: "Operação",
    items: [{ to: "/backup", label: "Backup", Icon: IconBackup }],
  },
  {
    id: "help",
    label: "Ajuda",
    items: [{ to: "/how-to", label: "Como usar", Icon: IconHowTo }],
  },
];

const adminGroup: NavGroup = {
  id: "admin",
  label: "Administração",
  items: [
    { to: "/admin/users", label: "Usuários", Icon: IconUsers },
    { to: "/admin/audit", label: "Auditoria", Icon: IconAudit },
    { to: "/admin/settings", label: "Configurações", Icon: IconSettings },
  ],
};

// ---- App ---------------------------------------------------------------------

export function App() {
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const close = () => setOpen(false);
  const auth = getAuth();
  const userLabel = auth?.email || auth?.user_id || "—";
  const userInitial = (auth?.email || "?").charAt(0).toUpperCase();
  const allGroups = auth?.role === "admin" ? [...groups, adminGroup] : groups;

  return (
    <div className="min-h-screen md:flex">
      {/* ===== Top bar (mobile only) ===== */}
      <header className="md:hidden flex items-center justify-between border-b border-border bg-muted px-4 py-3 sticky top-0 z-30">
        <h1 className="font-bold text-lg">ContextForge</h1>
        <div className="flex items-center gap-2">
          {auth && (
            <span className="text-xs text-slate-600 max-w-[140px] truncate" title={userLabel}>
              {userLabel}
            </span>
          )}
          <button
            aria-label="Abrir menu"
            className="p-2 rounded hover:bg-white"
            onClick={() => setOpen((v) => !v)}
          >
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              {open ? (
                <path d="M6 6l12 12M18 6L6 18" strokeLinecap="round" />
              ) : (
                <>
                  <path d="M3 6h18" strokeLinecap="round" />
                  <path d="M3 12h18" strokeLinecap="round" />
                  <path d="M3 18h18" strokeLinecap="round" />
                </>
              )}
            </svg>
          </button>
        </div>
      </header>

      {/* ===== Backdrop (mobile drawer) ===== */}
      {open && (
        <div
          className="md:hidden fixed inset-0 bg-black/40 z-30"
          onClick={close}
          aria-hidden
        />
      )}

      {/* ===== Sidebar ===== */}
      <aside
        className={
          "border-r border-border bg-muted p-3 flex flex-col " +
          "md:w-60 md:static md:translate-x-0 " +
          "fixed top-0 left-0 h-full w-64 z-40 transform transition-transform duration-200 " +
          (open ? "translate-x-0" : "-translate-x-full md:translate-x-0")
        }
      >
        <h1 className="font-bold text-lg mb-4 px-2 hidden md:block">ContextForge</h1>

        <nav className="flex-1 overflow-y-auto -mr-1 pr-1">
          {allGroups.map((g, idx) => (
            <div key={g.id} className={idx > 0 ? "mt-3" : ""}>
              {g.label && (
                <div className="mb-1 px-2 text-[10.5px] font-semibold uppercase tracking-wider text-slate-500">
                  {g.label}
                </div>
              )}
              <ul className="flex flex-col gap-0.5">
                {g.items.map((l) => (
                  <li key={l.to}>
                    <NavLink
                      to={l.to}
                      onClick={close}
                      className={({ isActive }) =>
                        "flex items-center gap-2 px-2.5 py-2 rounded text-sm transition-colors " +
                        (isActive
                          ? "bg-primary text-white"
                          : "text-slate-700 hover:bg-white")
                      }
                    >
                      <l.Icon className="shrink-0 opacity-80" />
                      <span className="truncate">{l.label}</span>
                    </NavLink>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>

        {auth && (
          <div className="mt-3 pt-3 border-t border-border">
            <div className="flex items-center gap-2 px-2 py-1.5">
              <div
                className="w-8 h-8 rounded-full bg-primary text-white flex items-center justify-center text-sm font-semibold shrink-0"
                aria-hidden
              >
                {userInitial}
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium truncate" title={userLabel}>
                  {userLabel}
                </div>
                <div className="text-xs text-slate-500 capitalize">{auth.role || "user"}</div>
              </div>
            </div>
            <button
              onClick={() => {
                clearAuth();
                navigate("/login");
              }}
              className="flex items-center gap-2 w-full text-sm text-left px-2.5 py-2 mt-1 text-slate-700 hover:bg-white rounded"
            >
              <IconLogout className="shrink-0 opacity-80" />
              <span>Sair</span>
            </button>
          </div>
        )}
      </aside>

      <main className="flex-1 p-4 sm:p-6 overflow-auto min-w-0">
        <Outlet />
      </main>
    </div>
  );
}
