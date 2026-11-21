// Copying text that works everywhere Slidekick is served.
//
// navigator.clipboard only exists in secure contexts (HTTPS or localhost). On a
// LAN address over plain HTTP it is undefined, so fall back to the older
// execCommand("copy") on a selected, off-screen textarea, which browsers still
// allow inside a click. If that fails too, show the text selected in a modal so
// Ctrl+C / Cmd+C copies it.
import { copyModal } from "./modal";

/** Resolves "copied" when the text is on the clipboard, or "shown" when it was shown for copying by hand. */
export async function copyText(text: string, title = "Copy this link"): Promise<"copied" | "shown"> {
  if (window.isSecureContext && navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text);
      return "copied";
    } catch {
      /* denied: try the fallback */
    }
  }
  if (legacyCopy(text)) return "copied";
  await copyModal({ title, text });
  return "shown";
}

function legacyCopy(text: string): boolean {
  // Inside a modal <dialog> everything else is inert, so the textarea must live in it.
  const host = document.querySelector("dialog[open]") ?? document.body;
  const before = document.activeElement as HTMLElement | null;
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.cssText = "position:fixed;top:0;left:-9999px;opacity:0;";
  host.append(ta);
  ta.select();
  ta.setSelectionRange(0, text.length);
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  ta.remove();
  before?.focus?.();
  return ok;
}
