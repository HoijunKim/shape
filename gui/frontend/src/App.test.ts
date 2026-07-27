// @vitest-environment jsdom
//
// E12 first-launch auto-open. App mounts the help overlay once on first launch
// (HelpSeen() false) and marks it seen. The heavy children (Header/Explorer/
// ViewsMenu) are stubbed and HelpOverlay is replaced with a marker stub so this
// targets ONLY App's onMount wiring.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { tick } from "svelte";

const h = vi.hoisted(() => ({
  HelpSeen: vi.fn(() => Promise.resolve(false)),
  MarkHelpSeen: vi.fn(() => Promise.resolve()),
}));

vi.mock("../wailsjs/go/main/App", () => ({
  SchemaJSON: vi.fn(),
  OpenFileDialog: vi.fn(() => Promise.resolve("")),
  SaveText: vi.fn(),
  HelpSeen: h.HelpSeen,
  MarkHelpSeen: h.MarkHelpSeen,
}));
vi.mock("../wailsjs/runtime/runtime", () => ({ OnFileDrop: vi.fn(), OnFileDropOff: vi.fn() }));
vi.mock("./lib/Header.svelte", async () => ({ default: (await import("./__fixtures__/Empty.svelte")).default }));
vi.mock("./lib/explorer/Explorer.svelte", async () => ({ default: (await import("./__fixtures__/Empty.svelte")).default }));
vi.mock("./lib/explorer/ViewsMenu.svelte", async () => ({ default: (await import("./__fixtures__/Empty.svelte")).default }));
vi.mock("./lib/HelpOverlay.svelte", async () => ({ default: (await import("./__fixtures__/HelpStub.svelte")).default }));
vi.mock("./lib/explorer/store", () => ({
  explorer: { subscribe: (run: (s: any) => void) => { run({ status: "idle", path: "", tier: "", format: "" }); return () => {}; } },
}));

import App from "./App.svelte";

let target: HTMLElement;
let cmp: any = null;

beforeEach(() => {
  h.HelpSeen.mockReset().mockResolvedValue(false);
  h.MarkHelpSeen.mockReset().mockResolvedValue(undefined as any);
});
afterEach(() => { cmp?.$destroy(); cmp = null; target?.remove(); });

const flush = () => new Promise((r) => setTimeout(r, 0));

async function mount() {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new App({ target });
  await flush();
  await tick();
}

describe("App first-launch help (E12)", () => {
  it("opens the help overlay and marks it seen on first launch (HelpSeen false)", async () => {
    h.HelpSeen.mockResolvedValue(false);
    await mount();
    expect(target.querySelector(".help-stub-open"), "help auto-opens on first launch").toBeTruthy();
    expect(h.MarkHelpSeen).toHaveBeenCalled();
  });

  it("does NOT auto-open when help has already been seen (HelpSeen true)", async () => {
    // Mutation: the auto-open ignores HelpSeen (always opens) -> this fails.
    h.HelpSeen.mockResolvedValue(true);
    await mount();
    expect(target.querySelector(".help-stub-open")).toBeNull();
    expect(h.MarkHelpSeen).not.toHaveBeenCalled();
  });
});
