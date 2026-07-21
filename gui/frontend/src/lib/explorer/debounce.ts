/** Debounces `fn`: repeated `.call()`s within `ms` collapse into a single
 *  invocation using the latest args, `ms` after the last call.
 *
 *  `.cancel()` drops the pending call without firing it -- load-bearing for
 *  the FilterBar (E3 Task 7), which calls it in `onDestroy` so a filter
 *  armed against file A can't fire after the user has opened file B. */
export function debounce<A extends any[]>(
  fn: (...a: A) => void,
  ms: number,
): { call: (...a: A) => void; flush: () => void; cancel: () => void } {
  let handle: ReturnType<typeof setTimeout> | undefined;
  let lastArgs: A | undefined;

  function call(...a: A): void {
    lastArgs = a;
    if (handle !== undefined) clearTimeout(handle);
    handle = setTimeout(() => {
      handle = undefined;
      const args = lastArgs as A;
      lastArgs = undefined;
      fn(...args);
    }, ms);
  }

  function flush(): void {
    if (handle === undefined) return;
    clearTimeout(handle);
    handle = undefined;
    const args = lastArgs as A;
    lastArgs = undefined;
    fn(...args);
  }

  function cancel(): void {
    if (handle !== undefined) clearTimeout(handle);
    handle = undefined;
    lastArgs = undefined;
  }

  return { call, flush, cancel };
}
