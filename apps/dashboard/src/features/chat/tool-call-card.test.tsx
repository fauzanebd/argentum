// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { expect, it, vi, describe } from "vitest";

/**
 * The tool chip, for the one tool whose name was the label.
 *
 * `TOOL_META` is a list of tools somebody remembered to add, and the fallback
 * for everything else is the raw wire name. That fallback is right for a tool
 * nobody has written copy for yet and wrong for `load_skill`, which is the
 * *only* way a tenant can see that a procedure they wrote was used at all —
 * the settings screen can say what a procedure costs on every turn and cannot
 * say whether any turn opened one. A chip reading `load_skill` spends that
 * answer on a string the reader has to already know.
 *
 * The third case below is the one holding the line: the result event carries
 * an empty map for this tool (`load_skill` returns a framed string, not JSON,
 * so `chat_runner.go`'s unmarshal leaves nothing behind), so the second chip
 * of the pair has no name to show and must not invent one.
 */

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
}));

import { ToolCallCard } from "./tool-call-card";

describe("load_skill", () => {
  it("names the procedure the agent opened", () => {
    render(<ToolCallCard name="load_skill" payload={{ name: "Weekly revenue by branch" }} />);
    expect(screen.getByText(/Weekly revenue by branch/)).toBeInTheDocument();
  });

  it("does not show the wire name", () => {
    render(<ToolCallCard name="load_skill" payload={{ name: "Weekly revenue by branch" }} />);
    expect(screen.queryByText(/load_skill/)).not.toBeInTheDocument();
  });

  it("reads as a procedure when the result carried no name", () => {
    render(<ToolCallCard name="load_skill" payload={{}} />);
    expect(screen.getByText("Procedure")).toBeInTheDocument();
  });
});

/** The fallback itself is deliberate and stays: an unlabelled tool shows its
 *  real name rather than a guess at a friendly one. */
it("still falls back to the wire name for a tool with no copy", () => {
  render(<ToolCallCard name="some_new_tool" payload={{}} />);
  expect(screen.getByText("some_new_tool")).toBeInTheDocument();
});
