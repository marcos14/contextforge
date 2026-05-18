import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { setAuth } from "../auth";

export function LoginPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const navigate = useNavigate();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr(null);
    try {
      const r = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      if (!r.ok) throw new Error((await r.json()).error || r.statusText);
      const data = await r.json();
      setAuth(data);
      navigate("/dashboard");
    } catch (e: any) { setErr(e.message); }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-muted">
      <form onSubmit={submit} className="bg-white p-8 rounded-lg shadow w-96 space-y-3 border border-border">
        <h1 className="font-bold text-2xl text-center">ContextForge</h1>
        <input className="w-full border border-border rounded px-3 py-2" placeholder="Email"
               value={email} onChange={(e) => setEmail(e.target.value)} />
        <input className="w-full border border-border rounded px-3 py-2" placeholder="Senha" type="password"
               value={password} onChange={(e) => setPassword(e.target.value)} />
        {err && <div className="text-sm text-red-600">{err}</div>}
        <button className="w-full bg-primary text-white rounded py-2">Entrar</button>
      </form>
    </div>
  );
}
