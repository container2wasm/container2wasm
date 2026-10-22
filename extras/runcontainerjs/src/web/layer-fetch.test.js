import { describe, expect, it } from "vitest";
import {
    buildRangeRequest,
    createLayerConnection,
    layerReadableSize,
    shouldUseRangeFetch,
} from "./layer-fetch.js";

function headHeaders(overrides = {}) {
    return {
        ok: overrides.ok ?? true,
        get(name) {
            const map = {
                "Accept-Ranges": overrides.acceptRanges ?? "bytes",
                "Content-Length": String(overrides.contentLength ?? 1000),
            };
            return map[name] ?? null;
        },
    };
}

describe("shouldUseRangeFetch", () => {
    it("returns true when HEAD supports byte ranges and layer is not gzip", () => {
        expect(shouldUseRangeFetch(headHeaders(), 0)).toBe(true);
    });

    it("returns false when Accept-Ranges is missing", () => {
        expect(shouldUseRangeFetch(headHeaders({ acceptRanges: "none" }), 0)).toBe(false);
    });

    it("returns false when Content-Length is zero", () => {
        expect(shouldUseRangeFetch(headHeaders({ contentLength: 0 }), 0)).toBe(false);
    });

    it("returns false for gzip layers even with ranges", () => {
        expect(shouldUseRangeFetch(headHeaders(), 1)).toBe(false);
    });

    it("returns false when HEAD is not ok", () => {
        expect(shouldUseRangeFetch(headHeaders({ ok: false }), 0)).toBe(false);
    });
});

describe("buildRangeRequest", () => {
    it("builds inclusive byte range headers", () => {
        const req = buildRangeRequest("https://example/layer", 100, 100);
        expect(req.url).toBe("https://example/layer");
        expect(req.headers.Range).toBe("bytes=100-199");
        expect(req.method).toBe("GET");
    });
});

describe("createLayerConnection", () => {
    it("initializes a non-range layer connection", () => {
        const conn = createLayerConnection("https://example/layer", "abc", 0);
        expect(conn.address).toBe("https://example/layer");
        expect(conn.digest).toBe("abc");
        expect(conn.rangeCapable).toBe(false);
        expect(conn.respBodybuf).toBeNull();
    });
});

describe("layerReadableSize", () => {
    it("uses contentLength for range-capable connections", () => {
        const conn = createLayerConnection("https://example/layer", "abc", 0);
        conn.rangeCapable = true;
        conn.contentLength = 4096;
        expect(layerReadableSize(conn)).toBe(4096);
    });

    it("uses respBodybuf length for buffered connections", () => {
        const conn = createLayerConnection("https://example/layer", "abc", 0);
        conn.respBodybuf = new Uint8Array(512);
        expect(layerReadableSize(conn)).toBe(512);
    });
});
