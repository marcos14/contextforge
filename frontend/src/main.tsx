import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "./index.css";
import { App } from "./App";
import { LandingPage } from "./pages/LandingPage";
import { LoginPage } from "./pages/LoginPage";
import { ConnectionsPage } from "./pages/ConnectionsPage";
import { GroupsPage } from "./pages/GroupsPage";
import { ToolsPage } from "./pages/ToolsPage";
import { QueryStudioPage } from "./pages/QueryStudioPage";
import { TokensPage } from "./pages/TokensPage";
import { DashboardPage } from "./pages/DashboardPage";
import { ConnectClientsPage } from "./pages/ConnectClientsPage";
import { BackupPage } from "./pages/BackupPage";
import { UsersPage } from "./pages/UsersPage";
import { AuditPage } from "./pages/AuditPage";
import { SettingsPage } from "./pages/SettingsPage";
import { HowToPage } from "./pages/HowToPage";
import { PublicLayout } from "./components/PublicLayout";
import { RequireAuth } from "./auth";

const qc = new QueryClient();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Routes>
          {/* Public routes */}
          <Route path="/" element={<LandingPage />} />
          <Route path="/login" element={<LoginPage />} />
          <Route
            path="/how-to"
            element={<PublicLayout><HowToPage /></PublicLayout>}
          />
          <Route
            path="/connect"
            element={<PublicLayout><ConnectClientsPage /></PublicLayout>}
          />

          {/* Authenticated routes */}
          <Route element={<RequireAuth><App /></RequireAuth>}>
            <Route path="/dashboard" element={<DashboardPage />} />
            <Route path="/connections" element={<ConnectionsPage />} />
            <Route path="/groups" element={<GroupsPage />} />
            <Route path="/tools" element={<ToolsPage />} />
            <Route path="/query-studio" element={<QueryStudioPage />} />
            <Route path="/tokens" element={<TokensPage />} />
            <Route path="/backup" element={<BackupPage />} />
            <Route path="/admin/users" element={<UsersPage />} />
            <Route path="/admin/audit" element={<AuditPage />} />
            <Route path="/admin/settings" element={<SettingsPage />} />
          </Route>

          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>
);
