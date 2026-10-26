#!/usr/bin/env node
/**
 * Mint a short-lived ArtificialFlow access token for golden-demo bake-off / k6
 * using OAuth2 client_credentials against the configured OIDC provider.
 *
 * Writes token to OUT_FILE (default: .local/bakeoff.token) and prints only the path.
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";

const OUT_FILE = resolve(process.env.OUT_FILE || ".local/bakeoff.token");
const TOKEN_URL = process.env.OIDC_TOKEN_URL || "";
const CLIENT_ID = process.env.OIDC_CLIENT_ID || "";
const CLIENT_SECRET = process.env.OIDC_CLIENT_SECRET || "";
const SCOPE = process.env.OIDC_SCOPE || "openid";

async function main() {
  if (!TOKEN_URL || !CLIENT_ID || !CLIENT_SECRET) {
    throw new Error("Set OIDC_TOKEN_URL, OIDC_CLIENT_ID, and OIDC_CLIENT_SECRET");
  }

  const body = new URLSearchParams({
    grant_type: "client_credentials",
    client_id: CLIENT_ID,
    client_secret: CLIENT_SECRET,
  });
  if (SCOPE) {
    body.set("scope", SCOPE);
  }

  const tokenRes = await fetch(TOKEN_URL, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded",
    },
    body,
  });
  let tokenBody;
  try {
    tokenBody = JSON.parse(await tokenRes.text());
  } catch {
    throw new Error("token exchange returned a non-JSON response");
  }
  if (!tokenRes.ok || !tokenBody.access_token) {
    throw new Error(`token exchange failed: ${tokenRes.status}`);
  }

  mkdirSync(dirname(OUT_FILE), { recursive: true });
  writeFileSync(OUT_FILE, tokenBody.access_token, { mode: 0o600 });
  console.log(OUT_FILE);
}

main().catch((err) => {
  console.error(err.message || err);
  process.exit(1);
});
