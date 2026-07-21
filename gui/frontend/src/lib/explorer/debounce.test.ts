import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { debounce } from "./debounce";

describe("debounce", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("coalesces calls within the window: fires fn once with the latest args after ms elapses", () => {
    const fn = vi.fn();
    const d = debounce(fn, 100);
    d.call(1);
    d.call(2);
    vi.advanceTimersByTime(99);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith(2);
  });

  it("cancel() before the timer elapses fires fn zero times", () => {
    const fn = vi.fn();
    const d = debounce(fn, 100);
    d.call(1);
    d.cancel();
    vi.advanceTimersByTime(1000);
    expect(fn).not.toHaveBeenCalled();
  });

  it("flush() fires fn immediately with the latest args and clears the pending timer", () => {
    const fn = vi.fn();
    const d = debounce(fn, 100);
    d.call(1);
    d.call(2);
    d.flush();
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith(2);
    // pending timer must be cleared -- advancing time must not fire again
    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("a call after a completed fire schedules a fresh fire (not swallowed)", () => {
    const fn = vi.fn();
    const d = debounce(fn, 100);
    d.call(1);
    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith(1);

    d.call(2);
    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledTimes(2);
    expect(fn).toHaveBeenCalledWith(2);
  });
});
