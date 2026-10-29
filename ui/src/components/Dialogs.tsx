import { useEffect } from "react";
import { refocusAfterDialog } from "@/lib/actions";
import { useStore } from "@/lib/store";
import { ConfirmDialog } from "./ConfirmDialog";
import { FilePicker, FileViewer } from "./FileViewer";
import { ImportHerdr } from "./ImportHerdr";
import { KeyHelp } from "./KeyHelp";
import { NewWorkspace, Rename } from "./NameDialogs";
import { Palette } from "./Palette";
import { SessionsDialog, WorkspacesDialog } from "./Pickers";
import { ResumeDialog } from "./ResumeDialog";
import { SettingsDialog } from "./SettingsDialog";

// Every store.dialog overlay, mounted once (App.tsx). Each dialog lives in its own file; store.dialog.kind picks which one is open.
export function Dialogs() {
  const d = useStore((s) => s.dialog);
  const open = !!d;
  // Closing any dialog (palette, settings, …) puts the cursor back in the focused pane's input
  useEffect(() => {
    if (!open) refocusAfterDialog();
  }, [open]);
  return (
    <>
      <Palette open={d?.kind === "palette"} />
      <KeyHelp open={d?.kind === "help"} />
      <SettingsDialog open={d?.kind === "settings"} />
      <SessionsDialog open={d?.kind === "sessions"} />
      <WorkspacesDialog open={d?.kind === "workspaces"} />
      <Rename />
      <NewWorkspace open={d?.kind === "newWorkspace"} />
      <ImportHerdr open={d?.kind === "importHerdr"} />
      <ResumeDialog />
      <FileViewer />
      <FilePicker />
      <ConfirmDialog />
    </>
  );
}
