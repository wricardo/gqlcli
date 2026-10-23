import { encode } from "@toon-format/toon";

export const OUTPUT_FORMATS = ["toon", "json", "json-pretty", "compact", "table", "llm"] as const;
export type OutputFormat = (typeof OUTPUT_FORMATS)[number];

/** Drops null/undefined object members and empty-null array items, recursively. */
export function stripNulls(v: unknown): unknown {
  if (Array.isArray(v)) return v.filter((x) => x !== null && x !== undefined).map(stripNulls);
  if (v && typeof v === "object") {
    return Object.fromEntries(Object.entries(v).filter(([, x]) => x !== null && x !== undefined).map(([k, x]) => [k, stripNulls(x)]));
  }
  return v;
}

function cell(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v).replace(/\s+/g, " ");
}

/** Aligned plain-text columns for an array of objects, or key/value rows for one object. */
function table(value: unknown): string {
  const rows = Array.isArray(value) ? value : [value];
  if (rows.length > 0 && rows.every((r) => r && typeof r === "object" && !Array.isArray(r))) {
    const objs = rows as Record<string, unknown>[];
    const cols = [...new Set(objs.flatMap((r) => Object.keys(r)))];
    const lines = [cols.map((c) => c.toUpperCase()), ...objs.map((r) => cols.map((c) => cell(r[c])))];
    const widths = cols.map((_, i) => Math.max(...lines.map((l) => (l[i] ?? "").length)));
    return lines.map((l) => l.map((c, i) => c.padEnd(widths[i] ?? 0)).join("  ").trimEnd()).join("\n");
  }
  return rows.map(cell).join("\n");
}

/** Markdown for LLM consumption: tables for arrays of flat objects, JSON fences otherwise. */
function markdown(value: unknown, depth = 0): string {
  if (Array.isArray(value) && value.length > 0 && value.every((r) => r && typeof r === "object" && !Array.isArray(r) && Object.values(r).every((x) => x === null || typeof x !== "object"))) {
    const objs = value as Record<string, unknown>[];
    const cols = [...new Set(objs.flatMap((r) => Object.keys(r)))];
    return [`| ${cols.join(" | ")} |`, `| ${cols.map(() => "---").join(" | ")} |`, ...objs.map((r) => `| ${cols.map((c) => cell(r[c]).replaceAll("|", "\\|")).join(" | ")} |`)].join("\n");
  }
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return Object.entries(value)
      .map(([k, v]) => (v && typeof v === "object" ? `${"#".repeat(Math.min(depth + 2, 6))} ${k}\n\n${markdown(v, depth + 1)}` : `- **${k}**: ${cell(v)}`))
      .join("\n\n");
  }
  return "```json\n" + JSON.stringify(value, null, 2) + "\n```";
}

/** Renders any JSON-compatible value. Strings pass through unchanged, so SDL text is never re-quoted. */
export function formatOutput(value: unknown, format: OutputFormat = "toon"): string {
  if (typeof value === "string") return value;
  if (value === undefined) return "";
  switch (format) {
    case "json":
      return JSON.stringify(value);
    case "json-pretty":
      return JSON.stringify(value, null, 2);
    case "compact":
      return JSON.stringify(stripNulls(value));
    case "table":
      return table(value);
    case "llm":
      return markdown(value);
    case "toon":
      return encode(value);
  }
}
