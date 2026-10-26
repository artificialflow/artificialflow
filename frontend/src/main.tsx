import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { AuthProvider } from "react-oidc-context";
import { WebStorageStateStore } from "oidc-client-ts";
import './index.css'
import App from './App.tsx'
import { runtimeConfig } from './lib/runtimeConfig';

const authority = runtimeConfig.oidcAuthority;
const clientID = runtimeConfig.oidcClientId;

function bootstrap() {
  const iamMode = runtimeConfig.iamMode;
  const oidcConfigured = Boolean(authority && clientID);
  const authConfigured = iamMode === true || (iamMode === null && oidcConfigured);
  const oidcConfig = {
    authority,
    client_id: clientID,
    redirect_uri: window.location.origin,
    silent_redirect_uri: window.location.origin,
    post_logout_redirect_uri: window.location.origin,
    response_type: "code",
    scope: "openid profile email",
    automaticSilentRenew: true,
    includeIdTokenInSilentRenew: true,
    monitorSession: false,
    // Survive hard refresh better than default sessionStorage.
    userStore: new WebStorageStateStore({ store: window.localStorage }),
    onSigninCallback: () => {
      // Avoid rewriting history inside the silent-renew iframe.
      if (window.self !== window.top) {
        return;
      }
      window.history.replaceState({}, document.title, window.location.pathname + window.location.hash);
    },
  };

  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      {authConfigured && !oidcConfigured ? (
        <div className="flex h-screen items-center justify-center p-6 text-center text-sm text-muted-foreground">
          IAM_DEPLOYMENT_MODE=true but FRONTEND_AUTH_OIDC_AUTHORITY / FRONTEND_AUTH_OIDC_CLIENT_ID are not set.
        </div>
      ) : authConfigured ? (
        <AuthProvider {...oidcConfig}>
          <App />
        </AuthProvider>
      ) : (
        <App authDisabled />
      )}
    </StrictMode>,
  )
}

bootstrap()
