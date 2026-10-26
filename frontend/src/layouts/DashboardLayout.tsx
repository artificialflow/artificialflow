import { Link, Navigate, Outlet, useLocation } from "react-router";
import { LayoutDashboard, Layers, Activity, ShieldUser, Menu, X, LogOut, History, Scale, AlertTriangle, BarChart3, type LucideIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import { api, type IdentityConfigResponse, type IdentityResponse } from "@/lib/api";
import { Button } from "@/components/ui/button";
import {
  ARTIFICIALFLOW_ADMIN_ROLE,
  ARTIFICIALFLOW_ANALYTICS_ROLE,
  ARTIFICIALFLOW_BUSINESS_ROLE,
  ARTIFICIALFLOW_CLIENT_ROLE,
  isAdmin,
  isClientOnly,
} from "@/lib/roles";
import { isDashboardPathAllowed } from "@/lib/dashboardAccess";
import { CqrsStatusBanner } from "@/components/CqrsStatusBanner";
import { ConsoleAccessDenied } from "@/components/ConsoleAccessDenied";
import { RUNTIME_UI_CAPABILITIES } from "@/lib/runtime-capabilities";

type SidebarItem = { icon: LucideIcon; label: string; href: string };

const sidebarItems: SidebarItem[] = [
  { icon: LayoutDashboard, label: "Dashboard", href: "/" },
  { icon: Layers, label: "Processes", href: "/processes" },
  ...(RUNTIME_UI_CAPABILITIES.decisions ? [{ icon: Scale, label: "Decisions", href: "/decisions" }] : []),
  { icon: Activity, label: "Instances", href: "/instances" },
  ...(RUNTIME_UI_CAPABILITIES.incidents ? [{ icon: AlertTriangle, label: "Incidents", href: "/incidents" }] : []),
  { icon: History, label: "History", href: "/history" },
  { icon: BarChart3, label: "Analytics", href: "/analytics" },
  { icon: ShieldUser, label: "Identity", href: "/identity" },
];

type DashboardLayoutProps = {
  onLogout?: () => void;
};

export default function DashboardLayout({ onLogout }: DashboardLayoutProps) {
  const [isSidebarOpen, setIsSidebarOpen] = useState(true);
  const [identity, setIdentity] = useState<IdentityResponse | null>(null);
  const [identityConfig, setIdentityConfig] = useState<IdentityConfigResponse | null>(null);
  const [loadingAccess, setLoadingAccess] = useState(true);
  const location = useLocation();
  const admin = isAdmin(identity);
  const clientOnly = isClientOnly(identity);
  const visibleSidebarItems = sidebarItems.filter((item) =>
    isDashboardPathAllowed({
      path: item.href,
      identity,
      config: identityConfig,
      showDecisionsUi: RUNTIME_UI_CAPABILITIES.decisions,
      showIncidentsUi: RUNTIME_UI_CAPABILITIES.incidents,
    }),
  );

  useEffect(() => {
    let cancelled = false;
    const loadIdentityAccess = async () => {
      try {
        const [identityResponse, configResponse] = await Promise.all([
          api.getIdentity(),
          api.getIdentityConfig(),
        ]);
        if (!cancelled) {
          setIdentity(identityResponse);
          setIdentityConfig(configResponse);
        }
      } catch {
        if (!cancelled) {
          setIdentity(null);
          setIdentityConfig(null);
        }
      } finally {
        if (!cancelled) {
          setLoadingAccess(false);
        }
      }
    };
    void loadIdentityAccess();
    return () => {
      cancelled = true;
    };
  }, []);

  if (loadingAccess) {
    return <div className="flex h-screen items-center justify-center">Loading access...</div>;
  }

  if (clientOnly) {
    return (
      <div className="flex h-screen items-center justify-center bg-muted/50 p-6">
        <div className="max-w-lg rounded-lg border bg-card p-6 text-center shadow-sm">
          <h1 className="text-2xl font-bold">Machine Client Access</h1>
          <p className="mt-3 text-sm text-muted-foreground">
            The {ARTIFICIALFLOW_CLIENT_ROLE} role is reserved for SDK, API and worker integrations.
            Sign in with an account that has {ARTIFICIALFLOW_ADMIN_ROLE}, {ARTIFICIALFLOW_BUSINESS_ROLE} or {ARTIFICIALFLOW_ANALYTICS_ROLE} for console access.
          </p>
          {onLogout && (
            <Button className="mt-6" variant="outline" onClick={onLogout}>
              <LogOut className="mr-2 h-4 w-4" />
              Logout
            </Button>
          )}
        </div>
      </div>
    );
  }

  const firstAllowedPath = visibleSidebarItems[0]?.href || "";
  const path = location.pathname;
  const isAllowedPath = isDashboardPathAllowed({
    path,
    identity,
    config: identityConfig,
    showDecisionsUi: RUNTIME_UI_CAPABILITIES.decisions,
    showIncidentsUi: RUNTIME_UI_CAPABILITIES.incidents,
  });

  if (path === "/" && firstAllowedPath && firstAllowedPath !== "/") {
    return <Navigate to={firstAllowedPath} replace />;
  }

  if (!isAllowedPath) {
    const message = `Your ArtificialFlow role does not allow this page. ${ARTIFICIALFLOW_ADMIN_ROLE} has full access, ${ARTIFICIALFLOW_BUSINESS_ROLE} can use Processes, Decisions and Modeler, and ${ARTIFICIALFLOW_ANALYTICS_ROLE} can use Analytics. Roles are assigned in your OIDC provider.`;
    return (
      <ConsoleAccessDenied
        message={message}
        firstAllowedPath={firstAllowedPath || undefined}
        onLogout={onLogout}
      />
    );
  }

  return (
    <div className="flex h-screen bg-background text-foreground overflow-hidden">
      {/* Sidebar */}
      <aside
        className={cn(
          "bg-card border-r transition-all duration-300 ease-in-out flex flex-col",
          isSidebarOpen ? "w-64" : "w-16"
        )}
      >
        <div className="h-16 flex items-center justify-between px-4 border-b">
          {isSidebarOpen && <span className="font-bold text-lg">ArtificialFlow</span>}
          <button
            onClick={() => setIsSidebarOpen(!isSidebarOpen)}
            className="p-1 hover:bg-accent rounded-md"
          >
            {isSidebarOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </div>

        <nav className="flex-1 py-4 flex flex-col gap-1 px-2">
          {visibleSidebarItems.map((item) => {
            const Icon = item.icon;
            const isActive = location.pathname === item.href;
            return (
              <Link
                key={item.href}
                to={item.href}
                className={cn(
                  "flex items-center gap-3 px-3 py-2 rounded-md transition-colors",
                  isActive
                    ? "bg-primary text-primary-foreground"
                    : "hover:bg-accent hover:text-accent-foreground",
                  !isSidebarOpen && "justify-center"
                )}
              >
                <Icon size={20} />
                {isSidebarOpen && <span>{item.label}</span>}
              </Link>
            );
          })}
        </nav>
      </aside>

      {/* Main Content */}
      <main className="flex-1 flex flex-col overflow-hidden">
        <header className="h-16 border-b flex items-center justify-between px-6 bg-card gap-4">
          <h1 className="text-xl font-semibold capitalize shrink-0">
            {location.pathname === "/"
              ? "Dashboard"
              : location.pathname.substring(1).replace("-", " ")}
          </h1>
          <div className="flex-1 flex justify-end items-center gap-4">
            <CqrsStatusBanner admin={admin} />
            {onLogout && (
              <Button variant="outline" size="sm" onClick={onLogout}>
                <LogOut className="mr-2 h-4 w-4" />
                Logout
              </Button>
            )}
          </div>
        </header>
        <div className="flex-1 overflow-auto p-6">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
