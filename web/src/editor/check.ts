// Client-side schema check: the editor validates the deck it is about to
// send with the same JSON Schema the server uses (pre-compiled Ajv), so a bad
// edit is explained instantly instead of after a round trip.
import { applyPatch } from "../../runtime/patch";
import type { Deck, PatchOp } from "../../runtime/types";
import validate from "./validate.gen.js";

/** The server assigns ids to new slides and elements; stand in for them here. */
function fillIds(v: unknown): void {
  if (Array.isArray(v)) return v.forEach(fillIds);
  if (!v || typeof v !== "object") return;
  const o = v as Record<string, unknown>;
  if (o.id === undefined && ("layout" in o || ("type" in o && "props" in o))) o.id = "pending";
  Object.values(o).forEach(fillIds);
}

function describe(path: string, message: string): string {
  const where = path
    .replace(/^\/slides\/(\d+)/, (_, i) => `slide ${Number(i) + 1}`)
    .replace(/\/regions\/([^/]+)\/(\d+)/, (_, r, i) => `, ${r} element ${Number(i) + 1}`)
    .replace(/\//g, " ")
    .trim();
  return where ? `${where}: ${message}` : message;
}

/** Problems with the deck after ops, or [] when it is valid (or the patch can't be applied locally). */
export function checkPatch(deck: Deck, ops: PatchOp[]): string[] {
  let next: unknown;
  try {
    next = applyPatch(structuredClone(deck), structuredClone(ops));
  } catch {
    return []; // let the server explain
  }
  fillIds(next);
  if (validate(next)) return [];
  const errs = (validate.errors ?? []).filter((e) => e.keyword !== "if" && e.keyword !== "allOf");
  return [...new Set(errs.map((e) => describe(e.instancePath, e.message ?? "is invalid")))];
}
