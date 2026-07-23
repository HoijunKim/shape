// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { tick } from "svelte";
import { get } from "svelte/store";
import CodegenPanel from "./CodegenPanel.svelte";
import { explorer } from "./store";
import { OpenSource, QueryRows, CloseSource, Cancel, Codegen } from "../../../wailsjs/go/main/App";
import { ClipboardSetText } from "../../../wailsjs/runtime";

vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(),
  QueryRows: vi.fn(),
  CloseSource: vi.fn(),
  Cancel: vi.fn(),
  CountMatches: vi.fn(),
  ExportQuery: vi.fn(),
  Codegen: vi.fn(),
}));
// The real runtime module dereferences window.runtime, which jsdom does not
// provide; the spy is also what the exact-text Copy assertion reads.
vi.mock("../../../wailsjs/runtime", () => ({
  ClipboardSetText: vi.fn(() => Promise.resolve(true)),
  EventsOn: vi.fn(() => () => {}),
}));

const openResult = (): any => ({
  handle: "h1", format: "ndjson", tier: "memory",
  columns: [{ path: "a", name: "a", type: "string", nullable: false, presence: 1, distinct: 1, container: false, index: 0 }],
  profile: { records: 1, skipped: 0, fields: [] },
  sampled: false, rowEstimate: 1, rowExact: true, warnings: [],
  columnsTruncated: false, totalPaths: 1,
});

const generated = (over: Record<string, unknown> = {}): any => ({
  jq: "# jq: one JSON object per line\nselect((.a? != null) // false)",
  sql: `SELECT * FROM "data" WHERE "a" IS NOT NULL;`,
  warnings: [],
  ...over,
});

let target: HTMLElement;
let panel: CodegenPanel | undefined;

const flush = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

beforeEach(async () => {
  target = document.createElement("div");
  document.body.appendChild(target);
  vi.mocked(OpenSource).mockReset().mockResolvedValue(openResult());
  vi.mocked(QueryRows).mockReset().mockResolvedValue({
    columns: [], rows: [], offset: 0, total: 1, totalExact: true,
    scanned: 0, truncated: true, elapsedMs: 0, columnsTruncated: false, totalPaths: 1,
  } as any);
  vi.mocked(CloseSource).mockReset().mockResolvedValue(undefined as any);
  vi.mocked(Cancel).mockReset().mockResolvedValue(undefined as any);
  vi.mocked(Codegen).mockReset().mockResolvedValue(generated());
  vi.mocked(ClipboardSetText).mockClear();
  await explorer.open("/data.ndjson");
  await flush();
});

afterEach(async () => {
  panel?.$destroy();
  panel = undefined;
  target.remove();
  await explorer.close();
});

function mount(props: Record<string, unknown> = {}): CodegenPanel {
  panel = new CodegenPanel({ target, props: { open: true, ...props } });
  return panel;
}

function button(label: string): HTMLButtonElement | undefined {
  return Array.from(target.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === label,
  ) as HTMLButtonElement | undefined;
}

describe("CodegenPanel", () => {
  it("renders the store's jq and SQL", async () => {
    mount();
    await tick();
    const jq = target.querySelector('[aria-label="jq program"]');
    const sql = target.querySelector('[aria-label="SQL query"]');
    expect(jq?.textContent).toContain("select(");
    expect(sql?.textContent).toContain(`SELECT * FROM "data"`);
  });

  it("copies the EXACT program, not the label or a trimmed version", async () => {
    mount();
    await tick();
    button("Copy")!.click();
    await flush();

    // Mutation that must break it: include the label, or trim/normalise the
    // text -> the copied string stops matching what the panel shows.
    expect(vi.mocked(ClipboardSetText)).toHaveBeenCalledWith(get(explorer).codegen!.jq);
  });

  it("renders the engine's caveats", async () => {
    vi.mocked(Codegen).mockResolvedValue(generated({
      warnings: ["regex matching differs by target: shape uses Go RE2"],
    }));
    await explorer.refreshCodegen();
    mount();
    await tick();
    expect(target.querySelector(".warning")?.textContent).toContain("RE2");
  });

  it("keeps the last good output when a refresh fails", async () => {
    mount();
    await tick();
    const before = get(explorer).codegen!.sql;

    vi.mocked(Codegen).mockRejectedValueOnce(new Error("boom"));
    await explorer.refreshCodegen();
    await tick();

    expect(target.querySelector(".error")?.textContent).toContain("boom");
    // Mutation that must break it: clear `codegen` on failure -> the panel
    // goes blank on a transient error instead of showing a stale-but-real
    // program.
    expect(target.querySelector('[aria-label="SQL query"]')?.textContent).toBe(before);
  });

  it("renders nothing when closed", () => {
    mount({ open: false });
    expect(target.querySelector(".codegen-panel")).toBeNull();
  });
});

describe("codegen refresh triggers", () => {
  it("re-renders after a filter change, sending the NEW filter", async () => {
    vi.mocked(Codegen).mockClear();
    explorer.setFilter({ combinator: "and", conditions: [{ path: "a", op: "notnull" }] } as any);
    await flush();

    // Asserting on the ARGUMENT, not just on $explorer.codegen: with a
    // constant mock the state assertion passes even if the refresh never ran.
    const sent = vi.mocked(Codegen).mock.calls.at(-1)?.[0] as any;
    expect(sent.filter.conditions).toHaveLength(1);
  });

  it("re-renders after a transform change, sending the NEW transform", async () => {
    vi.mocked(Codegen).mockClear();
    explorer.setTransform({ select: [{ path: "a", as: "A" }] } as any, [
      { path: "a", name: "A", type: "string", nullable: false, presence: 1, distinct: 1, container: false, index: 0 } as any,
    ]);
    await flush();

    // Mutation that must break it: drop the refreshCodegen() call from
    // setTransform -> the last request still carries the OLD transform.
    const sent = vi.mocked(Codegen).mock.calls.at(-1)?.[0] as any;
    expect(sent.transform.select).toHaveLength(1);
    expect(sent.transform.select[0].as).toBe("A");
  });

  it("is cleared by close()", async () => {
    expect(get(explorer).codegen).not.toBeNull();
    await explorer.close();
    expect(get(explorer).codegen).toBeNull();
    expect(get(explorer).codegenError).toBe("");
  });
});
