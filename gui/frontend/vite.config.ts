import {defineConfig} from 'vitest/config'
import {svelte} from '@sveltejs/vite-plugin-svelte'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [svelte()],
  test: {
    // M-4: "1,234 rows"/"showing 1,234 of 5,678 columns"-style assertions
    // (StatusBar.test.ts, rowCount.test.ts) rely on Number.prototype
    // .toLocaleString()'s NO-ARGUMENT form, which formats using the host's
    // default ICU locale -- e.g. de-DE renders "1.234", not "1,234". Pin it
    // to en-US for the whole suite here rather than passing an explicit
    // locale into each individual .toLocaleString() call, so the tests
    // exercise the same "no explicit locale" code path production code
    // uses, just under a locale guaranteed not to vary by contributor/CI
    // machine.
    //
    // Caveat verified while implementing this: on native Windows, Node's ICU
    // resolves the default locale from the OS user locale via the Win32
    // API, ignoring LANG/LC_ALL entirely (confirmed empirically -- setting
    // either had zero effect on `Intl.NumberFormat().resolvedOptions()
    // .locale` on this platform). This pin is therefore a no-op on Windows;
    // it takes effect on the POSIX platforms (Linux/macOS) ICU does read
    // these from, which is what most CI runners and contributor machines
    // outside Windows use.
    env: { LANG: 'en-US', LC_ALL: 'en-US' },
  },
})
