import { describe, expect, it } from "vitest";
import * as entry from "../src/index";

// workerd treats every named export of the main module as an entrypoint and
// refuses to start if one is not a handler/class ("Incorrect type for map
// entry ..."). The vitest pool does not enforce this, so `npm test` stayed
// green while `wrangler dev`/`wrangler deploy` could not boot the Worker.
describe("main module exports", () => {
  it("exports only entrypoints workerd can load", () => {
    for (const [name, value] of Object.entries(entry)) {
      const isHandler =
        typeof value === "function" ||
        (typeof value === "object" && value !== null && typeof (value as { fetch?: unknown }).fetch === "function");
      expect(isHandler, `export "${name}" is a ${typeof value}, not a handler`).toBe(true);
    }
    expect(typeof entry.default.fetch).toBe("function");
  });
});
