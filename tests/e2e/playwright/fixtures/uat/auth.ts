import type { Page } from "@playwright/test";

export type UatDeployment = "external-keycloak";

export interface UatLoginOptions {
  username?: string;
  password?: string;
}

export async function loginForDeployment(page: Page, deployment: UatDeployment, options: UatLoginOptions = {}): Promise<void> {
  console.log(`[uat-auth] goto frontend for ${deployment}`);
  await page.goto("/", { waitUntil: "domcontentloaded" });

  const signIn = page.getByRole("button", { name: /sign in/i });
  if (!(await signIn.count())) {
    console.log("[uat-auth] already authenticated or auth disabled");
    await page.waitForLoadState("domcontentloaded").catch(() => undefined);
    return;
  }

  console.log("[uat-auth] clicking sign in");
  await signIn.click();
  await loginKeycloak(page, options);

  console.log("[uat-auth] waiting for frontend callback");
  await page.waitForURL((url) => String(url).startsWith(process.env.FRONTEND_URL || "http://localhost:9100"), {
    timeout: 60_000,
  });
  await page.waitForFunction(hasStoredAccessToken, null, { timeout: 60_000 }).catch(() => undefined);
  console.log("[uat-auth] frontend callback complete");
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.locator("body").waitFor({ state: "visible", timeout: 30_000 });
}

export async function accessTokenFromBrowser(page: Page): Promise<string> {
  const token = await page.evaluate(() => {
    for (const store of [localStorage, sessionStorage]) {
      for (let i = 0; i < store.length; i += 1) {
        const key = store.key(i);
        const raw = key ? store.getItem(key) : null;
        if (!raw) continue;
        try {
          const parsed = JSON.parse(raw);
          if (parsed && typeof parsed.access_token === "string") {
            return parsed.access_token;
          }
        } catch {
          // Ignore non-JSON storage entries.
        }
      }
    }
    return "";
  });
  if (!token) {
    throw new Error("No OIDC access token is available in the authenticated browser session");
  }
  return token;
}

async function loginKeycloak(page: Page, options: UatLoginOptions): Promise<void> {
  await page.locator("#username").fill(options.username || process.env.UAT_USERNAME || "admin");
  await page.locator("#password").fill(options.password || process.env.UAT_PASSWORD || "admin");
  await page.locator("#kc-login").click();
}

function hasStoredAccessToken(): boolean {
  for (const store of [localStorage, sessionStorage]) {
    for (let i = 0; i < store.length; i += 1) {
      const key = store.key(i);
      const raw = key ? store.getItem(key) : null;
      if (!raw) continue;
      try {
        const parsed = JSON.parse(raw);
        if (parsed && typeof parsed.access_token === "string") {
          return true;
        }
      } catch {
        // Ignore non-JSON storage entries.
      }
    }
  }
  return false;
}
