import { useEffect, useRef, useState } from "react";

// dragover keeps firing roughly every 350ms (±200ms) while files hover the page,
// so a quiet period longer than that means the drag ended without a dragleave.
const STALE_DRAG_MS = 800;

function isFileDrag(event: DragEvent) {
  return Array.from(event.dataTransfer?.types ?? []).includes("Files");
}

/**
 * Accepts files dropped anywhere in the window while enabled, and blocks the
 * browser from navigating to a file dropped outside a target. Returns whether
 * files are currently dragged over the window.
 */
export function useFileDrop(enabled: boolean, onFiles: (files: File[]) => void): boolean {
  const [dragging, setDragging] = useState(false);
  const onFilesRef = useRef(onFiles);

  useEffect(() => {
    onFilesRef.current = onFiles;
  });

  useEffect(() => {
    if (!enabled) return;
    let depth = 0;
    let staleTimer: ReturnType<typeof setTimeout> | undefined;
    const stop = () => {
      depth = 0;
      clearTimeout(staleTimer);
      setDragging(false);
    };
    const enter = (event: DragEvent) => {
      if (!isFileDrag(event)) return;
      event.preventDefault();
      depth++;
      setDragging(true);
    };
    const over = (event: DragEvent) => {
      if (!isFileDrag(event)) return;
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
      setDragging(true);
      clearTimeout(staleTimer);
      staleTimer = setTimeout(stop, STALE_DRAG_MS);
    };
    const leave = (event: DragEvent) => {
      if (!isFileDrag(event)) return;
      depth = Math.max(0, depth - 1);
      if (depth === 0) stop();
    };
    const drop = (event: DragEvent) => {
      if (!isFileDrag(event)) return;
      event.preventDefault();
      stop();
      const files = Array.from(event.dataTransfer?.files ?? []);
      if (files.length) onFilesRef.current(files);
    };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragover", over);
    window.addEventListener("dragleave", leave);
    window.addEventListener("drop", drop);
    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragover", over);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("drop", drop);
      stop();
    };
  }, [enabled]);

  return dragging;
}
