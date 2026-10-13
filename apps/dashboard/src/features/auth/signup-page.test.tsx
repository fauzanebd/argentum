// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * The signup page (RIZ-79 R9, R10): it says new workspaces start with free
 * credit, and a refused sign-up from a busy address is shown in the form's
 * own error line with the button usable again.
 */

const navigate = vi.fn();
const post = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
  Link: ({ children }: { children?: React.ReactNode }) => <a>{children}</a>,
}));
vi.mock("@/lib/api", () => ({ api: { post: (...args: unknown[]) => post(...args) } }));

import { SignupPage } from "./signup-page";

const FLOOD = "Too many sign-ups from this network. Please try again in an hour.";

function fillAndSubmit() {
  fireEvent.change(screen.getByLabelText("Company name"), { target: { value: "Toko Segar Cabang 2" } });
  fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ops@tokosegar.id" } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct-horse" } });
  fireEvent.click(screen.getByRole("button", { name: "Create account" }));
}

beforeEach(() => {
  navigate.mockReset();
  post.mockReset();
});

describe("SignupPage", () => {
  it("says free credit follows approval, without naming an amount (RIZ-89 R15)", () => {
    render(<SignupPage />);
    expect(screen.getByText("You'll be the admin of your company workspace.")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Free credit to try Argentum is added once we've approved your new workspace. No card needed.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/start with free credit/)).toBeNull();
    expect(screen.queryByText(/\$\d/)).toBeNull();
  });

  it("shows the 429 sentence under the fields and re-enables the button (AC6)", async () => {
    post.mockRejectedValue(
      Object.assign(new Error("Request failed with status code 429"), {
        response: { status: 429, data: { error: FLOOD } },
      }),
    );
    render(<SignupPage />);
    fillAndSubmit();

    const line = await screen.findByText(FLOOD);
    expect(line.className).toContain("text-destructive");
    await waitFor(() =>
      expect((screen.getByRole("button", { name: "Create account" }) as HTMLButtonElement).disabled).toBe(false),
    );
    expect(navigate).not.toHaveBeenCalled();
  });

  it("still lands on onboarding after a successful sign-up", async () => {
    post.mockResolvedValue({ data: { access_token: "t", user: { id: "u-1", email: "ops@tokosegar.id" } } });
    render(<SignupPage />);
    fillAndSubmit();
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/onboarding" }));
    expect(post).toHaveBeenCalledWith("/auth/signup", {
      company_name: "Toko Segar Cabang 2",
      email: "ops@tokosegar.id",
      password: "correct-horse",
    });
  });
});
