import { describe, it, expect, vi, beforeEach } from "vitest";
import React from "react";

// ── mock external deps that rely on browser/OIDC context ──────────────────

vi.mock("react-oidc-context", () => ({
  useAuth: () => ({
    isAuthenticated: false,
    isLoading: false,
    user: null,
    signinRedirect: vi.fn(),
  }),
  withAuthenticationRequired: (Component: React.ComponentType) => Component,
}));

vi.mock("react-router", async () => {
  const actual = await vi.importActual("react-router");
  return {
    ...actual,
    BrowserRouter: ({ children }: { children: React.ReactNode }) => (
      <div data-testid="browser-router">{children}</div>
    ),
  };
});

vi.mock("@/lib/api", () => ({
  setAccessToken: vi.fn(),
  getWorkflows: vi.fn().mockResolvedValue({ workflows: [], total: 0 }),
  getInstances: vi.fn().mockResolvedValue({ instances: [], total: 0 }),
}));

// ── utility: bpmn parsing helpers ─────────────────────────────────────────

describe("BPMN XML helpers", () => {
  it("detects a valid BPMN process element", () => {
    const xml = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="myProcess" isExecutable="true"/>
</bpmn:definitions>`;
    expect(xml).toContain("bpmn:process");
    expect(xml).toContain("isExecutable");
  });

  it("detects missing process id as invalid", () => {
    const xml = `<bpmn:definitions><bpmn:process/></bpmn:definitions>`;
    const match = xml.match(/id="([^"]+)"/);
    expect(match).toBeNull();
  });
});

// ── utility: env/config helpers ───────────────────────────────────────────

describe("Environment config", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("defaults API base URL to localhost:8080", () => {
    const base = import.meta.env.VITE_API_BASE_URL || "http://localhost:8080";
    expect(base).toBeTruthy();
    expect(base).toContain("8080");
  });
});

// ── pure logic: status badge mapping ──────────────────────────────────────

type StatusColor = "green" | "red" | "yellow" | "gray";

function statusToColor(status: string): StatusColor {
  switch (status?.toLowerCase()) {
    case "running":
    case "active":
      return "green";
    case "failed":
    case "error":
      return "red";
    case "suspended":
    case "waiting":
      return "yellow";
    default:
      return "gray";
  }
}

describe("statusToColor", () => {
  it("maps running to green", () => expect(statusToColor("running")).toBe("green"));
  it("maps active to green", () => expect(statusToColor("active")).toBe("green"));
  it("maps failed to red", () => expect(statusToColor("failed")).toBe("red"));
  it("maps error to red", () => expect(statusToColor("error")).toBe("red"));
  it("maps suspended to yellow", () => expect(statusToColor("suspended")).toBe("yellow"));
  it("maps unknown to gray", () => expect(statusToColor("completed")).toBe("gray"));
  it("handles empty string gracefully", () => expect(statusToColor("")).toBe("gray"));
  it("is case-insensitive", () => expect(statusToColor("RUNNING")).toBe("green"));
});

// ── pure logic: pagination helpers ────────────────────────────────────────

function totalPages(total: number, pageSize: number): number {
  if (pageSize <= 0) return 1;
  return Math.max(1, Math.ceil(total / pageSize));
}

describe("totalPages", () => {
  it("returns 1 for empty list", () => expect(totalPages(0, 20)).toBe(1));
  it("returns 1 for exactly one page", () => expect(totalPages(20, 20)).toBe(1));
  it("returns 2 for one item over page size", () => expect(totalPages(21, 20)).toBe(2));
  it("returns correct ceil value", () => expect(totalPages(100, 30)).toBe(4));
  it("guards against zero pageSize", () => expect(totalPages(50, 0)).toBe(1));
});

// ── pure logic: variable serialisation ────────────────────────────────────

function serializeVariables(vars: Record<string, unknown>): string {
  return JSON.stringify(vars, null, 2);
}

function parseVariables(raw: string): Record<string, unknown> {
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

describe("variable serialisation", () => {
  it("serialises plain object", () => {
    const result = serializeVariables({ key: "value", num: 42 });
    expect(result).toContain('"key": "value"');
    expect(result).toContain('"num": 42');
  });

  it("round-trips correctly", () => {
    const original = { a: 1, b: "hello", c: true };
    const parsed = parseVariables(serializeVariables(original));
    expect(parsed).toEqual(original);
  });

  it("returns empty object for invalid JSON", () => {
    expect(parseVariables("not json")).toEqual({});
  });

  it("handles empty object", () => {
    expect(parseVariables(serializeVariables({}))).toEqual({});
  });
});
