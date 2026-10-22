import { describe, expect, it } from "vitest";
import { applyQemuMemoryOptions } from "./layer-fetch.js";

describe("applyQemuMemoryOptions", () => {
    it("sets initialMemory when provided", () => {
        const module = {};
        applyQemuMemoryOptions(module, { initialMemory: 2147483648 });
        expect(module.initialMemory).toBe(2147483648);
    });

    it("enables growth and sets maximumMemory when provided", () => {
        const module = {};
        applyQemuMemoryOptions(module, { maximumMemory: 4294967296 });
        expect(module.allowedToGrow).toBe(true);
        expect(module.maximumMemory).toBe(4294967296);
    });

    it("does not overwrite existing maximumMemory on module", () => {
        const module = { maximumMemory: 1024 };
        applyQemuMemoryOptions(module, { maximumMemory: 4294967296 });
        expect(module.allowedToGrow).toBe(true);
        expect(module.maximumMemory).toBe(1024);
    });

    it("no-ops when options are null", () => {
        const module = {};
        applyQemuMemoryOptions(module, null);
        expect(module.initialMemory).toBeUndefined();
    });
});
