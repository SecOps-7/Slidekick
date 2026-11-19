// Minimal RFC 6902 JSON Patch, applied to a deep copy. Mirrors what the server
// applies so clients can follow along without refetching the whole deck.
import type { PatchOp } from "./types";

type J = unknown;

function parse(ptr: string): string[] {
  if (ptr === "") return [];
  if (!ptr.startsWith("/")) throw new Error(`bad pointer ${ptr}`);
  return ptr.slice(1).split("/").map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
}

function parent(doc: J, path: string[]): [any, string] {
  let cur: any = doc;
  for (const k of path.slice(0, -1)) {
    cur = Array.isArray(cur) ? cur[Number(k)] : cur?.[k];
    if (cur === undefined) throw new Error(`path not found: /${path.join("/")}`);
  }
  return [cur, path[path.length - 1]];
}

function get(doc: J, ptr: string): J {
  let cur: any = doc;
  for (const k of parse(ptr)) cur = Array.isArray(cur) ? cur[Number(k)] : cur?.[k];
  return cur;
}

function add(doc: J, ptr: string, value: J): J {
  const p = parse(ptr);
  if (p.length === 0) return value;
  const [obj, key] = parent(doc, p);
  if (Array.isArray(obj)) obj.splice(key === "-" ? obj.length : Number(key), 0, value);
  else obj[key] = value;
  return doc;
}

function remove(doc: J, ptr: string): J {
  const [obj, key] = parent(doc, parse(ptr));
  if (Array.isArray(obj)) obj.splice(Number(key), 1);
  else delete obj[key];
  return doc;
}

const clone = <T>(v: T): T => structuredClone(v);

export function applyPatch<T>(doc: T, ops: PatchOp[]): T {
  let out: J = clone(doc);
  for (const op of ops) {
    switch (op.op) {
      case "add":
        out = add(out, op.path, clone(op.value));
        break;
      case "remove":
        out = remove(out, op.path);
        break;
      case "replace":
        out = add(remove(out, op.path), op.path, clone(op.value));
        break;
      case "move": {
        const v = get(out, op.from!);
        out = add(remove(out, op.from!), op.path, v);
        break;
      }
      case "copy":
        out = add(out, op.path, clone(get(out, op.from!)));
        break;
      case "test":
        if (JSON.stringify(get(out, op.path)) !== JSON.stringify(op.value)) throw new Error(`test failed at ${op.path}`);
        break;
    }
  }
  return out as T;
}
