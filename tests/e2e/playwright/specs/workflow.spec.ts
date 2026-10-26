import { test, expect, request } from "@playwright/test";

const COMMAND_URL = process.env.COMMAND_URL || "http://localhost:8080";

// ── API-level E2E tests (no browser required, but run in Playwright context) ──

test.describe("Workflow API — lifecycle", () => {
  test("health endpoint returns ok", async ({ request }) => {
    const res = await request.get(`${COMMAND_URL}/health`);
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(body.status).toBe("ready");
  });

  test("deploy workflow returns 200 with id", async ({ request }) => {
    const res = await request.post(`${COMMAND_URL}/workflows`, {
      data: minimalBPMN("e2e-playwright-deploy"),
      headers: { "Content-Type": "text/xml; charset=utf-8" },
    });
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(body.id).toBeTruthy();
  });

  test("deploy + start instance end-to-end", async ({ request }) => {
    // Deploy — raw BPMN XML body
    const deploy = await request.post(`${COMMAND_URL}/workflows`, {
      data: minimalBPMN("e2e-playwright-lifecycle"),
      headers: { "Content-Type": "text/xml; charset=utf-8" },
    });
    expect(deploy.status()).toBe(200);
    const { id: workflowId } = await deploy.json();

    // Start instance — uses workflow_id + context
    const start = await request.post(`${COMMAND_URL}/instances`, {
      data: { workflow_id: workflowId, context: { source: "playwright-e2e" } },
    });
    expect(start.status()).toBe(200);
    const inst = await start.json();
    expect(inst.id).toBeTruthy();

    // Fetch instance
    const get = await request.get(`${COMMAND_URL}/instances/${inst.id}`);
    expect(get.status()).toBe(200);
    const fetched = await get.json();
    expect(fetched.id).toBe(inst.id);
  });

  test("GET /workflows returns list", async ({ request }) => {
    const res = await request.get(`${COMMAND_URL}/workflows`);
    expect(res.status()).toBe(200);
  });

  test("GET /jobs/capabilities returns 200", async ({ request }) => {
    const res = await request.get(`${COMMAND_URL}/jobs/capabilities`);
    expect(res.status()).toBe(200);
  });

  test("GET /internal/metrics returns engine metrics JSON", async ({ request }) => {
    const res = await request.get(`${COMMAND_URL}/internal/metrics`);
    expect(res.status()).toBe(200);
    const ct = res.headers()["content-type"] || "";
    expect(ct).toContain("application/json");
    const body = await res.json();
    expect(body).toHaveProperty("outboxPublishLagSec");
  });
});

// ── Browser UI tests ────────────────────────────────────────────────────────

test.describe("Frontend UI", () => {
  test("home page loads", async ({ page }) => {
    await page.goto("/");
    await expect(page).not.toHaveTitle(/Error/);
    // App shell renders within 5s
    await page.waitForLoadState("networkidle");
    const body = await page.locator("body");
    await expect(body).toBeVisible();
  });

  test("page title is set", async ({ page }) => {
    await page.goto("/");
    const title = await page.title();
    expect(title).toBeTruthy();
  });
});

// ── Helpers ─────────────────────────────────────────────────────────────────

function minimalBPMN(processId: string): string {
  return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="${processId}" name="${processId}" isExecutable="true">
    <bpmn:startEvent id="start1"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="t1" name="Review"><bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing></bpmn:userTask>
    <bpmn:endEvent id="end1"><bpmn:incoming>f2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="end1"/>
  </bpmn:process>
</bpmn:definitions>`;
}
