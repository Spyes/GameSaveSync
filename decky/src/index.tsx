import {
  ButtonItem,
  PanelSection,
  PanelSectionRow,
  Spinner,
  staticClasses,
} from "@decky/ui";
import { callable, definePlugin, toaster } from "@decky/api";
import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { FaFloppyDisk } from "react-icons/fa6";

interface SyncItem {
  id: string;
  name: string;
  repoUrl: string;
  localPath: string;
}
interface ListResp { syncs?: SyncItem[]; error?: string }
interface StatusResp {
  statuses?: Record<string, { status: string; detail?: string }>;
  error?: string;
}
interface ActionResp { result?: string; error?: string }

// Bindings to the Python backend methods in main.py.
const listSyncs = callable<[], ListResp>("list_syncs");
const remoteStatus = callable<[], StatusResp>("remote_status");
const uploadSave = callable<[id: string, note: string], ActionResp>("upload");
const downloadSave = callable<[id: string, hash: string], ActionResp>("download");

const STATUS_LABEL: Record<string, string> = {
  "in-sync": "✓ In sync",
  "update-available": "⬇ Update available",
  "no-remote": "— not uploaded yet",
  error: "⚠ status unavailable",
};

function Text({ children }: { children: ReactNode }) {
  return <div style={{ fontSize: "0.85em", padding: "2px 0" }}>{children}</div>;
}

function Content() {
  const [syncs, setSyncs] = useState<SyncItem[] | null>(null);
  const [statuses, setStatuses] = useState<StatusResp["statuses"]>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string>("");

  const refresh = useCallback(async () => {
    const l = await listSyncs();
    if (l.error) {
      setError(l.error);
      setSyncs([]);
      return;
    }
    setError("");
    setSyncs(l.syncs ?? []);
    const s = await remoteStatus();
    if (!s.error && s.statuses) setStatuses(s.statuses);
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const act = async (id: string, kind: "upload" | "download") => {
    setBusy(`${id}:${kind}`);
    try {
      const r =
        kind === "upload" ? await uploadSave(id, "") : await downloadSave(id, "");
      toaster.toast({ title: "Save Sync", body: r.error ?? r.result ?? "Done" });
    } finally {
      setBusy(null);
      refresh();
    }
  };

  if (syncs === null) {
    return (
      <PanelSection>
        <PanelSectionRow>
          <Spinner />
        </PanelSectionRow>
      </PanelSection>
    );
  }

  return (
    <PanelSection title="Save Sync">
      {error !== "" && (
        <PanelSectionRow>
          <Text>Error: {error}</Text>
        </PanelSectionRow>
      )}
      {syncs.length === 0 && !error && (
        <PanelSectionRow>
          <Text>No games yet — add them in the desktop app, then reopen this.</Text>
        </PanelSectionRow>
      )}
      {syncs.map((s) => {
        const st = statuses?.[s.id]?.status ?? "";
        return (
          <PanelSection title={s.name} key={s.id}>
            <PanelSectionRow>
              <Text>{STATUS_LABEL[st] ?? "…"}</Text>
            </PanelSectionRow>
            <PanelSectionRow>
              <ButtonItem
                layout="below"
                disabled={busy !== null}
                onClick={() => act(s.id, "upload")}
              >
                {busy === `${s.id}:upload` ? "Uploading…" : "⬆ Upload save"}
              </ButtonItem>
            </PanelSectionRow>
            <PanelSectionRow>
              <ButtonItem
                layout="below"
                disabled={busy !== null}
                onClick={() => act(s.id, "download")}
              >
                {busy === `${s.id}:download` ? "Downloading…" : "⬇ Download save"}
              </ButtonItem>
            </PanelSectionRow>
          </PanelSection>
        );
      })}
      <PanelSectionRow>
        <ButtonItem layout="below" disabled={busy !== null} onClick={refresh}>
          Refresh
        </ButtonItem>
      </PanelSectionRow>
    </PanelSection>
  );
}

export default definePlugin(() => ({
  name: "Save Sync",
  titleView: <div className={staticClasses.Title}>Save Sync</div>,
  content: <Content />,
  icon: <FaFloppyDisk />,
  onDismount() {},
}));
