// Mega BPMN UAT: Supported matrix elements via API + light UI checks.
// Covers GitHub issue 40 quickstart path pieces (deploy/start) when AUTH is available.
// Env: ARTIFICIALFLOW_BASE_URL, ARTIFICIALFLOW_TOKEN, ARTIFICIALFLOW_END_USER, UAT_MEGA_SCALE
import { test, expect } from "@playwright/test";
import { readFileSync } from "fs";
import { join } from "path";

const base = (process.env.ARTIFICIALFLOW_BASE_URL || "http://localhost:9100/api").replace(/\/$/, "");
const token = process.env.ARTIFICIALFLOW_TOKEN || "";
const endUser = process.env.ARTIFICIALFLOW_END_USER || "approver";
const scale = Math.max(1, Number(process.env.UAT_MEGA_SCALE || 3));

const megaBpmn = readFileSync(join(__dirname, "../../fixtures/mega-process.bpmn"), "utf8");

async function api(method: string, path: string, body?: unknown, raw = false) {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${token}`,
  };
  if (!raw) headers["Content-Type"] = "application/json";
  else headers["Content-Type"] = "application/xml";
  const res = await fetch(`${base}${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : raw ? String(body) : JSON.stringify(body),
  });
  const text = await res.text();
  let json: any = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    json = { raw: text };
  }
  return { res, json, text };
}

test.describe("uat-mega-bpmn", () => {
  test.skip(!token, "ARTIFICIALFLOW_TOKEN required");

  test("deploy mega process, run worker+user task path, scale instances", async ({ page }) => {
    const deploy = await api("POST", "/workflows", megaBpmn, true);
    expect(deploy.res.status, deploy.text).toBeLessThan(300);
    const workflowId = deploy.json.id || deploy.json.workflow_id;
    expect(workflowId).toBeTruthy();

    const instanceIds: string[] = [];
    for (let i = 0; i < scale; i++) {
      const started = await api("POST", "/instances", {
        workflow_id: workflowId,
        context: { orderId: `uat-mega-${Date.now()}-${i}`, skipHuman: false },
      });
      expect(started.res.status, started.text).toBeLessThan(300);
      instanceIds.push(String(started.json.id));
    }

    // Complete service jobs
    for (let attempt = 0; attempt < 60; attempt++) {
      const activated = await api("POST", "/jobs/activate", {
        type: "uat-mega-validate",
        worker: "uat-mega-worker",
        maxJobs: 10,
        timeoutMs: 2000,
      });
      const jobs = activated.json?.jobs || [];
      for (const job of jobs) {
        const key = job.key || job.Key;
        await api("POST", `/jobs/${key}/complete`, {
          worker: "uat-mega-worker",
          variables: { validated: true },
        });
      }
      if (jobs.length === 0 && attempt > 5) break;
    }

    // Pending user task lookup, claim and complete for the first instance
    let matched = false;
    for (let i = 0; i < 30; i++) {
      const found = await api("GET", `/tasks?processInstanceKey=${instanceIds[0]}`);
      const task = found.json?.tasks?.[0];
      if (task) {
        const taskPath = `/instances/${task.processInstanceKey}/tasks/${task.executionId}`;
        if (task.canClaim) {
          await api("POST", `${taskPath}/claim`, { assignee: endUser });
        }
        const completed = await api("POST", `${taskPath}/complete`, { assignee: endUser });
        expect(completed.res.status, completed.text).toBe(200);
        matched = true;
        break;
      }
      await new Promise((r) => setTimeout(r, 1000));
    }
    expect(matched).toBeTruthy();

    // UI smoke: palette labels (modeler) when UI reachable
    await page.goto("http://localhost:9100/modeler?new=true", { waitUntil: "domcontentloaded" });
    // May redirect to login — only assert when modeler chrome is visible
    const palette = page.locator("text=Service Task");
    if (await palette.count()) {
      await expect(palette.first()).toBeVisible();
      const rail = page.locator("text=Events").locator("xpath=ancestor::div[contains(@class,'overflow')]").first();
      if (await rail.count()) {
        const overflowX = await rail.evaluate((el) => getComputedStyle(el).overflowX);
        expect(overflowX === "hidden" || overflowX === "auto" || overflowX === "scroll").toBeTruthy();
      }
    }
  });

  test("negative: unsupported ad-hoc subprocess reference fails deploy", async () => {
    const bad = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="D" targetNamespace="t">
  <bpmn:process id="bad_adhoc" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:adHocSubProcess id="adhoc"/>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="adhoc"/>
    <bpmn:sequenceFlow id="f2" sourceRef="adhoc" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`;
    const deploy = await api("POST", "/workflows", bad, true);
    expect(deploy.res.status).toBeGreaterThanOrEqual(400);
  });

  test("negative: complex gateway remains unsupported", async () => {
    const bad = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="D2" targetNamespace="t">
  <bpmn:process id="bad_complex" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:complexGateway id="cg"/>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="cg"/>
    <bpmn:sequenceFlow id="f2" sourceRef="cg" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`;
    const deploy = await api("POST", "/workflows", bad, true);
    expect(deploy.res.status).toBeGreaterThanOrEqual(400);
  });

  test("positive: escalation boundary deploys (Tier-2 Supported)", async () => {
    const xml = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="D3" targetNamespace="t">
  <bpmn:escalation id="Esc_1" escalationCode="NEED_HELP"/>
  <bpmn:process id="uat_escalation" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:userTask id="work"/>
    <bpmn:boundaryEvent id="esc" attachedToRef="work"><bpmn:escalationEventDefinition escalationRef="Esc_1"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="end"/>
    <bpmn:endEvent id="endEsc"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="work"/>
    <bpmn:sequenceFlow id="f2" sourceRef="work" targetRef="end"/>
    <bpmn:sequenceFlow id="f3" sourceRef="esc" targetRef="endEsc"/>
  </bpmn:process>
</bpmn:definitions>`;
    const deploy = await api("POST", "/workflows", xml, true);
    expect(deploy.res.status, deploy.text).toBeLessThan(300);
  });
});
