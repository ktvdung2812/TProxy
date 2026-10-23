import type { CLITool, CLIToolNote } from "../../cli-tools/constants";
import type { Tone } from "../page-kit";
import { Note } from "./CopyBits";
import { useToolCopy } from "./toolCopy";

const NOTE_TONE: Record<CLIToolNote["type"], Tone> = {
  info: "info",
  warning: "warning",
  cloudCheck: "warning",
  error: "danger",
};

/** The catalog's "good to know" notes; the cloud-reachability note only matters while setup is blocked on it. */
export function ToolNotes({ tool, includeCloudCheck = false }: { tool: CLITool; includeCloudCheck?: boolean }) {
  const toolCopy = useToolCopy();
  const notes = (tool.notes ?? []).filter((note) => note.type !== "cloudCheck" || includeCloudCheck);
  if (notes.length === 0) return null;
  return (
    <div className="ct-notes">
      {notes.map((note) => (
        <Note key={note.text} tone={NOTE_TONE[note.type]}>
          <p>{toolCopy.note(tool, note)}</p>
        </Note>
      ))}
    </div>
  );
}
