// Generates runtime/schema.gen.ts from schema/deck.schema.json, so Go (which
// validates against the schema), TypeScript and agents share one contract.
// runtime/types.ts builds its enums and props types on this file.
import { readFileSync, writeFileSync } from "node:fs";
import { compile } from "json-schema-to-typescript";

const schema = JSON.parse(readFileSync(new URL("../../schema/deck.schema.json", import.meta.url), "utf8"));
const ts = await compile(schema, "SlidekickDeck", {
  additionalProperties: false,
  unreachableDefinitions: true,
  maxItems: -1, // plain arrays, not tuples, for maxItems limits
  bannerComment: "/* Generated from schema/deck.schema.json by scripts/gen-types.mjs. Do not edit; run `npm run types`. */",
  format: true,
  style: { printWidth: 160 },
});
const out = new URL("../runtime/schema.gen.ts", import.meta.url);
const prev = (() => {
  try {
    return readFileSync(out, "utf8");
  } catch {
    return "";
  }
})();
if (prev !== ts) writeFileSync(out, ts);
console.log(`schema.gen.ts ${prev === ts ? "unchanged" : "written"}`);
