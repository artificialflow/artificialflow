import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";

describe("api.deployWorkflow", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("includes backend validation details from failed deploy responses", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response("bpmn validation failed: missing process", {
          status: 400,
          statusText: "Bad Request",
        }),
      ),
    );

    await expect(api.deployWorkflow("<invalid />")).rejects.toThrow(
      "bpmn validation failed: missing process",
    );
  });
});

describe("api.getWorkflows", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("reads the query-side workflow catalog", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        workflows: [
          {
            id: "workflow-1",
            process_definition_id: "Process_Order",
            name: "Order",
            version: 1,
            resource_name: "order.bpmn",
            deployment_id: "deployment-1",
            tenant_id: "default",
            resource_checksum: "checksum",
            bpmn_xml: "<xml />",
            created_at: new Date().toISOString(),
          },
        ],
        total: 1,
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const workflows = await api.getWorkflows();

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/query/workflows?page=1&pageSize=100",
      expect.objectContaining({
        headers: expect.objectContaining({
          "X-Correlation-ID": expect.any(String),
        }),
      }),
    );
    expect(workflows).toHaveLength(1);
    expect(workflows[0].process_definition_id).toBe("Process_Order");
  });
});
