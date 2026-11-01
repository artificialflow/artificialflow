import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ConsoleLogin from "./ConsoleLogin";

const respond = (status: number, body: unknown) =>
  vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));

describe("ConsoleLogin", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("posts the credentials and reports the signed-in principal", async () => {
    const fetchMock = respond(200, { authenticated: true, principal: { subject: "admin", roles: ["artificialflowadmin"] } });
    vi.stubGlobal("fetch", fetchMock);
    const onSignedIn = vi.fn();
    render(<ConsoleLogin onSignedIn={onSignedIn} />);

    await userEvent.type(screen.getByLabelText("Username"), "admin");
    await userEvent.type(screen.getByLabelText("Password"), "admin");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));

    expect(onSignedIn).toHaveBeenCalledWith(expect.objectContaining({ subject: "admin" }));
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toMatch(/\/identity\/login$/);
    expect(JSON.parse(init.body)).toEqual({ username: "admin", password: "admin" });
  });

  it("shows an error for wrong credentials", async () => {
    vi.stubGlobal("fetch", respond(401, {}));
    const onSignedIn = vi.fn();
    render(<ConsoleLogin onSignedIn={onSignedIn} />);

    await userEvent.type(screen.getByLabelText("Username"), "admin");
    await userEvent.type(screen.getByLabelText("Password"), "nope");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("Invalid username or password")).toBeTruthy();
    expect(onSignedIn).not.toHaveBeenCalled();
  });
});
