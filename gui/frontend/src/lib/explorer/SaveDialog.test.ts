// @vitest-environment jsdom
//
// E7 save-a-copy dialog. SaveDialog reads the save lifecycle off the explorer
// singleton and opens the native picker via App.SaveFileDialog; both are mocked
// so the four states (idle / saving / done / failed) can be driven directly.
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";

const h = vi.hoisted(() => ({
  state: {
    saving: false,
    saveResult: null as any,
    saveError: "",
    saveRows: 0,
    editedCount: 1,
    status: "ready",
    path: "C:/data.ndjson",
  },
  saveEdits: vi.fn(() => Promise.resolve()),
  dismissSave: vi.fn(),
}));

vi.mock("./store", () => ({
  explorer: {
    subscribe: (run: (s: any) => void) => {
      run(h.state);
      return () => {};
    },
    saveEdits: h.saveEdits,
    dismissSave: h.dismissSave,
  },
}));

vi.mock("../../../wailsjs/go/main/App", () => ({
  SaveFileDialog: vi.fn(() => Promise.resolve("C:/picked.ndjson")),
}));

import SaveDialog from "./SaveDialog.svelte";

let target: HTMLElement;
let cmp: any = null;

afterEach(() => {
  cmp?.$destroy();
  cmp = null;
  target?.remove();
  h.state = {
    saving: false, saveResult: null, saveError: "", saveRows: 0,
    editedCount: 1, status: "ready", path: "C:/data.ndjson",
  };
  h.saveEdits.mockClear();
  h.dismissSave.mockClear();
});

function mount(props: Record<string, unknown> = {}): HTMLElement {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new SaveDialog({ target, props: { open: true, ...props } });
  return target;
}

async function flush(): Promise<void> {
  await new Promise((r) => setTimeout(r, 0));
  await tick();
}

function button(label: string): HTMLButtonElement | undefined {
  return Array.from(target.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === label,
  ) as HTMLButtonElement | undefined;
}

describe("SaveDialog (E7)", () => {
  it("renders nothing when closed", () => {
    const t = mount({ open: false });
    expect(t.querySelector(".dialog")).toBeNull();
  });

  it("defaults the filename from the source stem with an -edited suffix", () => {
    const t = mount();
    const input = t.querySelector('[aria-label="Output file"]') as HTMLInputElement;
    expect(input.placeholder).toBe("data-edited.ndjson");
  });

  it("saves the chosen format to the typed path", async () => {
    const t = mount();
    const input = t.querySelector('[aria-label="Output file"]') as HTMLInputElement;
    input.value = "C:/out.ndjson";
    input.dispatchEvent(new Event("input"));
    await tick();
    button("Save")!.click();
    await flush();
    expect(h.saveEdits).toHaveBeenCalledWith("ndjson", "C:/out.ndjson");
  });

  it("disables Save when there are no edits", () => {
    h.state.editedCount = 0;
    mount();
    expect(button("Save")!.disabled).toBe(true);
  });

  it("reports unapplied edits on the done state instead of a clean success", () => {
    h.state.saveResult = {
      outPath: "C:/out.ndjson", rowsOut: 10, editsApplied: 1, editsUnapplied: 2,
      bytesOut: 2048, elapsedMs: 1, warnings: [],
    };
    const t = mount();
    // Mutation: drop the editsUnapplied block -> a save that silently dropped
    // two edits reports as a clean success.
    expect(t.querySelector(".counts")?.textContent).toContain("2 not applied");
    expect(t.querySelector(".counts .warn")).toBeTruthy();
  });

  it("shows a failure with Retry", () => {
    h.state.saveError = "permission denied";
    const t = mount();
    expect(t.querySelector(".state.failed")?.textContent).toContain("permission denied");
    expect(button("Retry")).toBeTruthy();
  });
});
