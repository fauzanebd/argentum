// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * The picture library screen (T-G12).
 *
 * What is worth testing here is not that a card renders. It is the two things
 * this screen exists to get right, both of which are about the **name**: the
 * name is what the model asks for, so an image nobody names is an image the
 * agent cannot use — and a member must be able to read the list while being
 * unable to change it, which is the rule this codebase settled on 2026-08-04
 * (a disabled control tells a member who to ask; a hidden one tells them the
 * feature does not exist).
 */

const get = vi.fn();
const post = vi.fn();
const del = vi.fn();
let admin = true;

vi.mock("@/lib/api", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
    delete: (...args: unknown[]) => del(...args),
  },
}));
vi.mock("@/store/auth", () => ({ useIsAdmin: () => admin }));
vi.mock("@/hooks/use-toast", () => ({ useToast: () => ({ toast: vi.fn() }) }));
vi.mock("@/lib/api-error", () => ({ apiErrorMessage: (e: Error) => e.message }));

import { ImagesTab } from "./images-tab";

const library = {
  images: [
    {
      id: "img-1",
      name: "Jeruk Cara Cara",
      alt: "Jeruk dibelah",
      width: 900,
      height: 600,
      byte_size: 51200,
      created_at: "2026-09-04T00:00:00Z",
    },
  ],
  limits: { max_bytes: 4 << 20, max_edge: 2048, max_name_chars: 80, max_alt_chars: 300 },
};

function renderTab() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ImagesTab />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  admin = true;
  get.mockReset();
  post.mockReset();
  del.mockReset();
  get.mockImplementation((path: string) => {
    if (path === "/post-images") return Promise.resolve({ data: library });
    // The image bytes, fetched through the API client because the route is
    // authenticated. jsdom has no createObjectURL, so it is stubbed.
    return Promise.resolve({ data: new Blob(["png"]) });
  });
  globalThis.URL.createObjectURL = vi.fn(() => "blob:stub");
  globalThis.URL.revokeObjectURL = vi.fn();
});

describe("the picture library", () => {
  it("lists what the workspace has, by the name the agent asks for", async () => {
    renderTab();
    expect(await screen.findByText("Jeruk Cara Cara")).toBeInTheDocument();
    expect(screen.getByText(/900×600/)).toBeInTheDocument();
  });

  it("suggests a name from the filename, so nothing ends up called IMG_2831", async () => {
    renderTab();
    await screen.findByText("Jeruk Cara Cara");

    const file = new File(["x"], "jeruk-cara-cara.jpg", { type: "image/jpeg" });
    fireEvent.change(screen.getByLabelText("File"), { target: { files: [file] } });

    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("jeruk cara cara");
  });

  it("refuses to upload without a name, because the name is the interface", async () => {
    renderTab();
    await screen.findByText("Jeruk Cara Cara");

    const add = screen.getByRole("button", { name: /add image/i });
    expect(add).toBeDisabled();

    const file = new File(["x"], "photo.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("File"), { target: { files: [file] } });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "" } });
    expect(add).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Kopi Susu" } });
    expect(add).toBeEnabled();
  });

  it("sends the file, the name and the description as one multipart form", async () => {
    post.mockResolvedValue({ data: { ...library.images[0], name: "Kopi Susu" } });
    renderTab();
    await screen.findByText("Jeruk Cara Cara");

    const file = new File(["x"], "kopi.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("File"), { target: { files: [file] } });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Kopi Susu" } });
    fireEvent.change(screen.getByLabelText("Description"), { target: { value: "Segelas kopi" } });
    fireEvent.click(screen.getByRole("button", { name: /add image/i }));

    await waitFor(() => expect(post).toHaveBeenCalled());
    const [path, body] = post.mock.calls[0] as [string, FormData];
    expect(path).toBe("/post-images");
    expect(body.get("name")).toBe("Kopi Susu");
    expect(body.get("alt")).toBe("Segelas kopi");
    expect(body.get("image")).toBeInstanceOf(File);
  });

  it("shows a member the library and disables every control that changes it", async () => {
    admin = false;
    renderTab();
    await screen.findByText("Jeruk Cara Cara");

    expect(screen.getByLabelText("File")).toBeDisabled();
    expect(screen.getByLabelText("Name")).toBeDisabled();
    expect(screen.getByRole("button", { name: /add image/i })).toBeDisabled();
    // Told who to ask, rather than shown an empty screen.
    expect(screen.getByText(/only an admin can add or remove images/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /remove/i })).not.toBeInTheDocument();
  });

  it("says what an empty library means for a promotion", async () => {
    get.mockImplementation((path: string) =>
      path === "/post-images"
        ? Promise.resolve({ data: { ...library, images: [] } })
        : Promise.resolve({ data: new Blob(["png"]) }),
    );
    renderTab();
    expect(await screen.findByText(/drawn as type on a coloured card/i)).toBeInTheDocument();
  });
});
