export function shouldUseRangeFetch(headHeaders, isGzipN) {
    const acceptRanges = headHeaders.get("Accept-Ranges");
    const contentLength = parseInt(headHeaders.get("Content-Length") || "0", 10);
    const headOk = headHeaders.ok !== false;
    return headOk && acceptRanges === "bytes" && contentLength > 0 && isGzipN == 0;
}

export function buildRangeRequest(url, offset, len) {
    const end = offset + len - 1;
    return {
        url,
        method: "GET",
        mode: "cors",
        credentials: "omit",
        headers: { Range: "bytes=" + offset + "-" + end },
    };
}

export function createLayerConnection(address, digest, isGzipN) {
    const request = {
        method: "GET",
        mode: "cors",
        credentials: "omit",
    };
    return {
        address,
        request,
        requestSent: false,
        reqBodybuf: new Uint8Array(0),
        reqBodyEOF: false,
        digest,
        isGzipN,
        rangeCapable: false,
        contentLength: 0,
        response: null,
        done: null,
        respBodybuf: null,
    };
}

export function applyQemuMemoryOptions(module, options) {
    if (options == null) {
        return;
    }
    if (options.initialMemory != null) {
        module.initialMemory = options.initialMemory;
    }
    if (options.maximumMemory != null) {
        module.allowedToGrow = true;
        if (typeof module.maximumMemory === "undefined") {
            module.maximumMemory = options.maximumMemory;
        }
    }
}

export function layerReadableSize(conn) {
    if (conn.rangeCapable) {
        return conn.contentLength;
    }
    return conn.respBodybuf ? conn.respBodybuf.byteLength : 0;
}
