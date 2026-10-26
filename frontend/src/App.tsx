import { lazy, Suspense, useEffect, useLayoutEffect, useState } from "react";
import { BrowserRouter, Route, Routes } from "react-router";
import { useAuth } from "react-oidc-context";
import { setAccessToken } from "@/lib/api";
import DashboardLayout from "@/layouts/DashboardLayout";
import { Loader2 } from "lucide-react";
import { RUNTIME_UI_CAPABILITIES } from "@/lib/runtime-capabilities";
import ConsoleLogin from "@/components/ConsoleLogin";

const Dashboard = lazy(() => import("@/pages/Dashboard"));
const Modeler = lazy(() => import("@/pages/Modeler"));
const Processes = lazy(() => import("@/pages/Processes"));
const Instances = lazy(() => import("@/pages/Instances"));
const InstanceDetails = lazy(() => import("@/pages/InstanceDetails"));
const History = lazy(() => import("@/pages/History"));
const Analytics = lazy(() => import("@/pages/Analytics"));
const Identity = lazy(() => import("@/pages/Identity"));

const Decisions = lazy(() => import("@/pages/Decisions"));
const Incidents = lazy(() => import("@/pages/Incidents"));

type AppProps = {
  authDisabled?: boolean;
};

type AppRoutesProps = {
  onLogout?: () => void;
};

function LoadingScreen() {
  return (
    <div className="flex h-screen items-center justify-center">
      <Loader2 className="h-8 w-8 animate-spin text-primary" />
    </div>
  );
}

function AppRoutes({ onLogout }: AppRoutesProps) {
  return (
    <BrowserRouter>
      <Suspense fallback={<LoadingScreen />}>
        <Routes>
          <Route path="/" element={<DashboardLayout onLogout={onLogout} />}>
            <Route index element={<Dashboard />} />
            <Route path="modeler" element={<Modeler />} />
            <Route path="processes" element={<Processes />} />
            {RUNTIME_UI_CAPABILITIES.decisions ? (
              <Route path="decisions" element={<Decisions />} />
            ) : null}
            <Route path="instances" element={<Instances />} />
            <Route path="instances/:id" element={<InstanceDetails />} />
            {RUNTIME_UI_CAPABILITIES.incidents ? (
              <Route path="incidents" element={<Incidents />} />
            ) : null}
            <Route path="history" element={<History />} />
            <Route path="analytics" element={<Analytics />} />
            <Route path="identity" element={<Identity />} />
          </Route>
        </Routes>
      </Suspense>
    </BrowserRouter>
  );
}

function AuthenticatedApp() {
  const auth = useAuth();
  const accessToken = auth.user?.access_token;

  useLayoutEffect(() => {
    if (auth.isAuthenticated && accessToken) {
      setAccessToken(accessToken);
      return;
    }
    // Keep the previous bearer during loading / silent renew so in-flight API
    // calls are not suddenly unauthenticated.
    if (!auth.isLoading && !auth.isAuthenticated) {
      setAccessToken(null);
    }
  }, [auth.isAuthenticated, auth.isLoading, accessToken]);

  if (auth.isLoading) {
    return <LoadingScreen />;
  }

  // Prefer an active session over a transient renew/error state. Showing a hard
  // "Oops" page used to force users through refresh → login.
  if (auth.isAuthenticated && accessToken) {
    if (auth.error) {
      console.warn("OIDC warning (session kept):", auth.error.message);
    }

    const logout = () => {
      setAccessToken(null);
      void auth.signoutRedirect({
        post_logout_redirect_uri: window.location.origin,
      });
    };

    return <AppRoutes onLogout={logout} />;
  }

  if (auth.error) {
    return (
      <div className="flex h-screen items-center justify-center bg-muted/50 p-6">
        <div className="max-w-md space-y-4 text-center">
          <h1 className="text-2xl font-bold">ArtificialFlow</h1>
          <p className="text-sm text-muted-foreground">
            Your session could not be renewed. Sign in again to continue.
          </p>
          <p className="text-xs text-muted-foreground break-words">{auth.error.message}</p>
          <button
            onClick={() => void auth.signinRedirect()}
            className="px-4 py-2 bg-primary text-primary-foreground rounded-md hover:bg-primary/90 transition-colors"
          >
            Sign in again
          </button>
        </div>
      </div>
    );
  }

  if (!auth.isAuthenticated) {
    return (
      <div className="flex h-screen items-center justify-center bg-muted/50">
        <div className="text-center space-y-4">
          <h1 className="text-2xl font-bold">ArtificialFlow</h1>
          <p className="text-muted-foreground">Please sign in to continue</p>
          <button
            onClick={() => void auth.signinRedirect()}
            className="px-4 py-2 bg-primary text-primary-foreground rounded-md hover:bg-primary/90 transition-colors"
          >
            Sign in
          </button>
        </div>
      </div>
    );
  }

  return <LoadingScreen />;
}

const CONSOLE_SESSION_KEY = "artificialflow.console-session";

function AuthDisabledApp() {
  const [signedIn, setSignedIn] = useState(() => window.sessionStorage.getItem(CONSOLE_SESSION_KEY) !== null);

  useEffect(() => {
    setAccessToken(null);
  }, []);

  if (!signedIn) {
    return (
      <ConsoleLogin
        onSignedIn={(principal) => {
          window.sessionStorage.setItem(CONSOLE_SESSION_KEY, principal.subject);
          setSignedIn(true);
        }}
      />
    );
  }

  const logout = () => {
    window.sessionStorage.removeItem(CONSOLE_SESSION_KEY);
    setSignedIn(false);
  };

  return <AppRoutes onLogout={logout} />;
}

function App({ authDisabled = false }: AppProps) {
  if (authDisabled) {
    return <AuthDisabledApp />;
  }

  return <AuthenticatedApp />;
}

export default App;
