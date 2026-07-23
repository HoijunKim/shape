// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { tick } from "svelte";
import ExportDialog from "./ExportDialog.svelte";
import { explorer } from "./store";
import { OpenSource, QueryRows, SaveFileDialog, CloseSource, Cancel } from "../../../wailsjs/go/main/App";

vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(),
  QueryRows: vi.fn(),
  CloseSource: vi.fn(),
  Cancel: vi.fn(),
  CountMatches: vi.fn(),
  // E5: store.ts calls Codegen; a bare vi.fn() resolves undefined and the
  // first property read on the result throws.
  Codegen: vi.fn(() => Promise.resolve({ jq: ".", sql: "SELECT * FROM data;", warnings: [] })),
  ExportQuery: vi.fn(),
  SaveFileDialog: vi.fn(),
}));
vi.mock("../../../wailsjs/runtime", () => ({ EventsOn: vi.fn(() => () => {}) }));

let target: HTMLElement;
let dialog: ExportDialog | undefined;
let runExport: ReturnType<typeof vi.spyOn>;
let cancelExport: ReturnType<typeof vi.spyOn>;

const openResult = (): any => ({
  handle: "h1", format: "json", tier: "memory",
  columns: [{ path: "a", name: "a", type: "string", nullable: false, presence: 1, distinct: 1, container: false, index: 0 }],
  profile: { records: 0, skipped: 0, fields: [] },
  sampled: false, rowEstimate: 12, rowExact: true, warnings: [],
  columnsTruncated: false, totalPaths: 1,
});

beforeEach(async () => {
  target = document.createElement("div");
  document.body.appendChild(target);
  vi.mocked(OpenSource).mockReset().mockResolvedValue(openResult());
  vi.mocked(QueryRows).mockReset().mockResolvedValue({
    columns: [], rows: [], offset: 0, total: 12, totalExact: true,
    scanned: 0, truncated: true, elapsedMs: 0, columnsTruncated: false, totalPaths: 1,
  } as any);
  vi.mocked(SaveFileDialog).mockReset().mockResolvedValue("C:/out.csv");
  vi.mocked(CloseSource).mockReset().mockResolvedValue(undefined as any);
  vi.mocked(Cancel).mockReset().mockResolvedValue(undefined as any);
  await explorer.open("/data.ndjson");
  runExport = vi.spyOn(explorer, "runExport").mockResolvedValue(undefined);
  cancelExport = vi.spyOn(explorer, "cancelExport").mockImplementation(() => {});
});

afterEach(async () => {
  dialog?.$destroy();
  dialog = undefined;
  target.remove();
  runExport.mockRestore();
  cancelExport.mockRestore();
  await explorer.close();
});

function mount(props: Record<string, unknown> = {}): ExportDialog {
  dialog = new ExportDialog({ target, props: { open: true, ...props } });
  return dialog;
}

// The dialog's handlers are async (the native picker is awaited before the
// export starts), so a single tick is not enough to see their effects: flush
// the microtask queue AND let Svelte apply the resulting update.
async function flush(): Promise<void> {
  await new Promise((r) => setTimeout(r, 0));
  await tick();
}

function button(label: string): HTMLButtonElement | undefined {
  return Array.from(target.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === label,
  ) as HTMLButtonElement | undefined;
}

describe("ExportDialog", () => {
  it("exports the chosen format to the chosen path", async () => {
    mount();
    const select = target.querySelector('[aria-label="Export format"]') as HTMLSelectElement;
    select.value = "csv";
    select.dispatchEvent(new Event("change"));
    await tick();

    button("Choose file…")!.click();
    await flush();
    expect(vi.mocked(SaveFileDialog).mock.calls[0][1]).toBe("csv");

    button("Export")!.click();
    await flush();
    expect(runExport).toHaveBeenCalledWith("csv", "C:/out.csv");
  });

  it("defaults the filename from the source and the format", async () => {
    mount();
    const input = target.querySelector('[aria-label="Output file"]') as HTMLInputElement;
    expect(input.placeholder).toBe("data-export.ndjson");
  });

  it("shows the live row count while exporting, with a Cancel", async () => {
    mount();
    // Drive the store into the exporting state via the real action.
    runExport.mockRestore();
    const { ExportQuery } = await import("../../../wailsjs/go/main/App");
    let resolve!: (v: any) => void;
    vi.mocked(ExportQuery).mockReturnValueOnce(new Promise((r) => (resolve = r)) as any);
    const p = explorer.runExport("ndjson", "C:/out.ndjson");
    await tick();

    expect(target.querySelector(".state.busy")?.textContent).toContain("0 rows written");
    expect(button("Cancel")).toBeTruthy();

    resolve({ outPath: "C:/out.ndjson", rowsOut: 5, bytesOut: 2048, elapsedMs: 1, warnings: [] });
    await p;
    await tick();
    expect(target.querySelector(".state.done")?.textContent).toContain("5 rows");
    expect(target.querySelector(".state.done")?.textContent).toContain("2.0 KB");
    expect(target.querySelector(".path")?.textContent).toBe("C:/out.ndjson");
  });

  it("renders the engine's fidelity warnings on the done state", async () => {
    runExport.mockRestore();
    const { ExportQuery } = await import("../../../wailsjs/go/main/App");
    vi.mocked(ExportQuery).mockResolvedValueOnce({
      outPath: "C:/out.parquet", rowsOut: 2, bytesOut: 100, elapsedMs: 1,
      warnings: ["3 value(s) did not fit their Parquet column type and were written as null (n)"],
    } as any);
    mount();
    await explorer.runExport("parquet", "C:/out.parquet");
    await tick();

    // Mutation that must break this: drop the warnings block -> a lossy export
    // reports as a clean success.
    expect(target.querySelector(".warning")?.textContent).toContain("written as null");
  });

  it("shows a failure with Retry", async () => {
    runExport.mockRestore();
    const { ExportQuery } = await import("../../../wailsjs/go/main/App");
    vi.mocked(ExportQuery).mockRejectedValueOnce(new Error("disk full"));
    mount();
    await explorer.runExport("csv", "C:/out.csv");
    await tick();

    expect(target.querySelector(".state.failed")?.textContent).toContain("disk full");
    expect(button("Retry")).toBeTruthy();
  });

  it("disables Export with the reason when the columns panel is invalid", async () => {
    mount({ disabledReason: 'Two columns are both named "x".' });
    await tick();
    expect(button("Export")!.disabled).toBe(true);
    expect(target.querySelector(".state.failed")?.textContent).toContain("both named");
  });

  it("Esc during an export CANCELS instead of closing", async () => {
    runExport.mockRestore();
    const { ExportQuery } = await import("../../../wailsjs/go/main/App");
    vi.mocked(ExportQuery).mockReturnValueOnce(new Promise(() => {}) as any);
    mount();
    let closed = false;
    dialog!.$on("close", () => (closed = true));
    void explorer.runExport("csv", "C:/out.csv");
    await tick();

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await tick();

    // Mutation that must break this: make Esc always close -> cancelExport is
    // never called and the export keeps running with no dialog to report it.
    expect(cancelExport).toHaveBeenCalled();
    expect(closed).toBe(false);
  });

  it("Esc when idle closes", async () => {
    mount();
    let closed = false;
    dialog!.$on("close", () => (closed = true));
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await tick();
    expect(closed).toBe(true);
  });

  it("renders nothing when closed", () => {
    mount({ open: false });
    expect(target.querySelector(".dialog")).toBeNull();
  });
});

// --- E4 branch review regressions -------------------------------------------

describe("ExportDialog review fixes", () => {
  it("warns when the chosen file's extension does not match the format, without blocking", async () => {
    mount();
    const input = target.querySelector('[aria-label="Output file"]') as HTMLInputElement;
    input.value = "C:/out.csv";
    input.dispatchEvent(new Event("input"));
    const select = target.querySelector('[aria-label="Export format"]') as HTMLSelectElement;
    select.value = "parquet";
    select.dispatchEvent(new Event("change"));
    await tick();

    // Advisory only: an odd name stays allowed, but writing Parquet bytes into
    // a .csv is almost always a slip -- and shape's own reader dispatches on
    // the extension, so the result would not re-open here.
    expect(target.querySelector(".note")?.textContent).toContain("csv");
    expect(button("Export")!.disabled).toBe(false);
  });

  it("accepts .jsonl for ndjson (the picker offers it)", async () => {
    mount();
    const input = target.querySelector('[aria-label="Output file"]') as HTMLInputElement;
    input.value = "C:/out.jsonl";
    input.dispatchEvent(new Event("input"));
    await tick();
    expect(target.querySelector(".note")).toBeNull();
  });

  it("moves focus into the dialog and hands it back on close", async () => {
    const opener = document.createElement("button");
    document.body.appendChild(opener);
    opener.focus();
    expect(document.activeElement).toBe(opener);

    mount();
    await tick();
    await tick();
    const dlg = target.querySelector(".dialog") as HTMLElement;
    expect(dlg.contains(document.activeElement) || document.activeElement === dlg).toBe(true);

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await tick();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it("pulls escaped focus back into the dialog on Tab", async () => {
    const outside = document.createElement("button");
    document.body.appendChild(outside);
    mount();
    await tick();
    outside.focus(); // focus escaped behind the backdrop

    const ev = new KeyboardEvent("keydown", { key: "Tab", cancelable: true });
    window.dispatchEvent(ev);
    await tick();
    const dlg = target.querySelector(".dialog") as HTMLElement;
    expect(dlg.contains(document.activeElement)).toBe(true);
    outside.remove();
  });
});
