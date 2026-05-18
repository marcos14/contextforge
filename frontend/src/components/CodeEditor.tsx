import { useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import CodeMirror from "@uiw/react-codemirror";
import { sql } from "@codemirror/lang-sql";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";

export type EditorLanguage = "sql" | "javascript" | "json";

export type SqlSchema = Record<string, string[]>;

type Props = {
  value: string;
  onChange: (v: string) => void;
  language: EditorLanguage;
  height?: string;
  placeholder?: string;
  /** Optional table->columns map fed to the SQL autocomplete. */
  sqlSchema?: SqlSchema;
  /** Extra label shown in the toolbar (e.g. "Query"). */
  label?: string;
  /** Disable editing. */
  readOnly?: boolean;
};

/**
 * CodeEditor wraps CodeMirror 6 with SQL/JS language support, autocomplete,
 * line numbers and a fullscreen modal toggle. It is intentionally light: no
 * theme override (uses the default light theme) and no custom keymaps.
 */
export function CodeEditor({
  value,
  onChange,
  language,
  height = "10rem",
  placeholder,
  sqlSchema,
  label,
  readOnly,
}: Props) {
  const [fullscreen, setFullscreen] = useState(false);

  // Close fullscreen on Esc.
  useEffect(() => {
    if (!fullscreen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setFullscreen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [fullscreen]);

  const extensions = useMemo(() => {
    if (language === "javascript") return [javascript({ jsx: false, typescript: false })];
    if (language === "json") return [json()];
    return [sql({ schema: sqlSchema })];
  }, [language, sqlSchema]);

  const editor = (full: boolean) => (
    <CodeMirror
      value={value}
      onChange={(v) => onChange(v)}
      extensions={extensions}
      height={full ? "calc(100vh - 7rem)" : height}
      placeholder={placeholder}
      readOnly={readOnly}
      basicSetup={{
        lineNumbers: true,
        foldGutter: true,
        autocompletion: true,
        highlightActiveLine: true,
        bracketMatching: true,
        closeBrackets: true,
        indentOnInput: true,
      }}
      theme="light"
    />
  );

  return (
    <div className="border border-border rounded overflow-hidden bg-white">
      <div className="flex items-center justify-between px-2 py-1 border-b border-border bg-muted/40 text-xs">
        <span className="text-muted-foreground">
          {label ?? (language === "javascript" ? "JavaScript" : language === "json" ? "JSON" : "SQL")}
        </span>
        <button
          type="button"
          className="px-2 py-0.5 border border-border rounded bg-white hover:bg-muted"
          onClick={() => setFullscreen(true)}
          title="Abrir em tela cheia"
        >
          ⛶ Expandir
        </button>
      </div>
      {editor(false)}

      {fullscreen &&
        createPortal(
          <div
            className="fixed inset-0 bg-black/50 z-50 flex items-center justify-center p-4"
            onClick={() => setFullscreen(false)}
          >
            <div
              className="bg-white rounded shadow-xl w-full h-full max-w-[1400px] flex flex-col overflow-hidden"
              onClick={(e) => e.stopPropagation()}
            >
              <div className="flex items-center justify-between border-b border-border px-4 py-2">
                <div className="font-semibold text-sm">
                  {label ?? (language === "javascript" ? "JavaScript" : language === "json" ? "JSON" : "SQL")}{" "}
                  <span className="text-muted-foreground font-normal">— tela cheia</span>
                </div>
                <button
                  type="button"
                  className="text-sm px-3 py-1 border border-border rounded hover:bg-muted"
                  onClick={() => setFullscreen(false)}
                  title="Fechar (Esc)"
                >
                  Fechar
                </button>
              </div>
              <div className="flex-1 overflow-auto">{editor(true)}</div>
            </div>
          </div>,
          document.body,
        )}
    </div>
  );
}
