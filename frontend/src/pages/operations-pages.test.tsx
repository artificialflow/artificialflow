import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Decisions from "./Decisions";
import Incidents from "./Incidents";
import InstanceDetails from "./InstanceDetails";

const apiMock = vi.hoisted(() => ({
  listDecisions: vi.fn(),
  deployDecision: vi.fn(),
  evaluateDecision: vi.fn(),
  listIncidents: vi.fn(),
  retryJob: vi.fn(),
  resolveIncident: vi.fn(),
  getInstance: vi.fn(),
  getIdentity: vi.fn(),
  listInstanceJobs: vi.fn(),
  getWorkflow: vi.fn(),
  migrateInstance: vi.fn(),
}));

vi.mock("@/lib/api", () => ({ api: apiMock }));

describe("productized operations pages", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.listDecisions.mockResolvedValue([]);
    apiMock.listIncidents.mockResolvedValue([]);
    apiMock.retryJob.mockResolvedValue(undefined);
    apiMock.resolveIncident.mockResolvedValue({});
    apiMock.getIdentity.mockResolvedValue({
      authenticated: true,
      principal: { subject: "admin", roles: ["artificialflowadmin"] },
    });
    apiMock.listInstanceJobs.mockResolvedValue([]);
    apiMock.getWorkflow.mockResolvedValue({
      id: "10", process_definition_id: "orders", name: "Orders", version: 1, bpmn_xml: "",
    });
    apiMock.getInstance.mockResolvedValue({
      id: "100",
      workflow_id: "10",
      status: "RUNNING",
      context: {},
      executions: [],
      created_at: "2026-08-06T00:00:00Z",
      updated_at: "2026-08-06T00:00:00Z",
    });
  });

  it("renders an accessible Decisions empty state", async () => {
    render(<Decisions />);
    expect(await screen.findByText("No decisions deployed yet.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Upload decision" })).toBeEnabled();
  });

  it("announces Decisions API failures", async () => {
    apiMock.listDecisions.mockRejectedValue(new Error("permission denied"));
    render(<Decisions />);
    expect(await screen.findByRole("alert")).toHaveTextContent("permission denied");
  });

  it("renders an accessible Incidents empty state", async () => {
    render(<MemoryRouter><Incidents /></MemoryRouter>);
    expect(screen.getByRole("status")).toHaveTextContent("Loading incidents");
    expect(await screen.findByText("No incidents require attention.")).toBeInTheDocument();
  });

  it("announces Incidents API failures", async () => {
    apiMock.listIncidents.mockRejectedValue(new Error("forbidden"));
    render(<MemoryRouter><Incidents /></MemoryRouter>);
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Failed to load incidents"));
  });

  it("exposes accessible retry and resolve actions", async () => {
    const incident = {
      key: "10", id: "incident-10", processInstanceKey: "20", elementInstanceKey: "30",
      jobKey: "40", errorType: "JOB_FAILED", errorMessage: "failed", state: "CREATED",
      createdAt: "2026-08-06T00:00:00Z",
    };
    apiMock.listIncidents.mockResolvedValue([incident]);
    apiMock.resolveIncident.mockResolvedValue({ ...incident, state: "RESOLVED" });
    render(<MemoryRouter><Incidents /></MemoryRouter>);

    const retry = await screen.findByRole("button", { name: "Retry job 40 for incident 10" });
    fireEvent.click(retry);
    await waitFor(() => expect(apiMock.retryJob).toHaveBeenCalledWith("40"));
    expect(await screen.findByRole("status")).toHaveTextContent("scheduled for retry");

    fireEvent.click(screen.getByRole("button", { name: "Resolve incident 10" }));
    await waitFor(() => expect(apiMock.resolveIncident).toHaveBeenCalledWith("10"));
    expect(await screen.findByRole("status")).toHaveTextContent("resolved");
  });

  it("announces incident action failures", async () => {
    const incident = {
      key: "10", id: "incident-10", processInstanceKey: "20", elementInstanceKey: "30",
      errorType: "SLA", errorMessage: "late", state: "CREATED", createdAt: "2026-08-06T00:00:00Z",
    };
    apiMock.listIncidents.mockResolvedValue([incident]);
    apiMock.resolveIncident.mockRejectedValue(new Error("resolve forbidden"));
    render(<MemoryRouter><Incidents /></MemoryRouter>);
    fireEvent.click(await screen.findByRole("button", { name: "Resolve incident 10" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("resolve forbidden");
  });

  it("carries the migration operation ID from dry run to apply and links support guidance", async () => {
    apiMock.migrateInstance
      .mockResolvedValueOnce({
        operationId: "migration-100",
        instanceKey: "100",
        dryRun: true,
        status: "VALIDATED",
      })
      .mockResolvedValueOnce({
        operationId: "migration-100",
        instanceKey: "100",
        dryRun: false,
        status: "APPLIED",
      });

    render(
      <MemoryRouter initialEntries={["/instances/100"]}>
        <Routes>
          <Route path="/instances/:id" element={<InstanceDetails />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByText("Instance Migration")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Support and undo guidance" })).toHaveAttribute(
      "href",
      "https://github.com/artificialflow/artificialflow/blob/main/docs/INSTANCE_MIGRATION.md",
    );
    fireEvent.change(screen.getByPlaceholderText("Target definition key"), { target: { value: "20" } });
    fireEvent.click(screen.getByRole("button", { name: "Dry run" }));
    await waitFor(() => expect(apiMock.migrateInstance).toHaveBeenCalledWith(
      "100",
      {
        sourceDefinitionKey: "10",
        targetDefinitionKey: "20",
        elementMapping: {},
        operationId: undefined,
      },
      true,
    ));
    expect(await screen.findByDisplayValue("migration-100")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(apiMock.migrateInstance).toHaveBeenLastCalledWith(
      "100",
      {
        sourceDefinitionKey: "10",
        targetDefinitionKey: "20",
        elementMapping: {},
        operationId: "migration-100",
      },
      false,
    ));
  });
});
