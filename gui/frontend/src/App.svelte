<script lang="ts">
  import { onMount } from "svelte";
  import { ProfileFile, SchemaJSON, OpenFileDialog, SaveText } from "../wailsjs/go/main/App";
  import { OnFileDrop, OnFileDropOff } from "../wailsjs/runtime/runtime";
  import type { main } from "../wailsjs/go/models";
  import Header from "./lib/Header.svelte";
  import FileDrop from "./lib/FileDrop.svelte";
  import ProfileTable from "./lib/ProfileTable.svelte";
  import FieldDetail from "./lib/FieldDetail.svelte";

  let profile: main.ProfileView | null = null;
  let selected: main.FieldView | null = null;
  let loading = false;
  let error = "";

  async function load(path: string) {
    if (!path) return;
    loading = true;
    error = "";
    profile = null;
    selected = null;
    try {
      profile = await ProfileFile(path);
      selected = profile.fields?.[0] ?? null;
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  }

  async function open() {
    await load(await OpenFileDialog());
  }

  async function exportSchema() {
    if (!profile) return;
    try {
      await SaveText("schema.json", await SchemaJSON(profile.source));
    } catch (e) {
      error = String(e);
    }
  }

  function selectField(field: main.FieldView) {
    selected = field;
  }

  onMount(() => {
    OnFileDrop((_x, _y, paths) => {
      if (paths?.length) load(paths[0]);
    }, true);
    return () => OnFileDropOff();
  });
</script>

<main class="app">
  <Header
    source={profile?.source ?? ""}
    records={profile?.records ?? 0}
    skipped={profile?.skipped ?? 0}
    canExport={!!profile}
    on:open={open}
    on:export={exportSchema}
  />
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if loading}
    <p class="hint">Profiling...</p>
  {:else if !profile}
    <FileDrop on:open={open} />
  {:else}
    <div class="split">
      <ProfileTable fields={profile.fields} {selected} on:select={(e) => selectField(e.detail)} />
      {#if selected}<FieldDetail field={selected} />{/if}
    </div>
  {/if}
</main>
