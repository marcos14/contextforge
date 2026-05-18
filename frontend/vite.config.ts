import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

export default defineConfig(({ mode }: { mode: string }) => {
  const env = loadEnv(mode, path.resolve(__dirname, ".."), "");
  const devPort = Number(env.VITE_DEV_PORT ?? 5173);
  const backendUrl = env.VITE_BACKEND_URL ?? "http://localhost:8080";
  return {
    plugins: [react()],
    resolve: {
      alias: { "@": path.resolve(__dirname, "./src") },
    },
    server: {
      port: devPort,
      proxy: {
        "/api": backendUrl,
        "/mcp": backendUrl,
      },
    },
  };
});
