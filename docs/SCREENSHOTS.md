# Screenshots (GitHub #34)

Refresh README and getting-started screenshots after UI changes on the `v0.4` train.

## Capture checklist

With `make up` and an OIDC admin sign-in:

1. **Dashboard** — CQRS banner visible when lag metrics exist.
2. **Modeler** — left palette shows **Events / Tasks / Gateways** labels (not icon-only); properties panel shows editable **ID**.
3. **Processes** — catalog after deploy.
4. **Analytics** — placeholder page for an `artificialflowanalytics` account.
5. **Instances** — job ops / retry controls for admins.

Store PNGs under `docs/images/` (create if missing) and link from [README.md](../README.md) and [getting-started.md](getting-started.md).

Do not commit screenshots that contain tokens, PATs, or private keys.
