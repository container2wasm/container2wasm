var accepted = false;
let curSocket = null;
let eventQueue = [];
let stackWorker = null;

// Mimimum WebSocket mock which implements only fields used by the emscripten runtime.
// TODO: Add more fields
function emscriptenMockWebSocket(address, onconnection) {
    const WindowWebSocket = window.WebSocket;

    class EmscriptenMockWebSocket {
        constructor(url, protocols) {
            this.url = url;
            this.protocol = protocols;
            this.binaryType = "arraybuffer";
            this.CONNECTING = 0;
            this.OPEN = 1;
            this.CLOSING = 2;
            this.CLOSED = 3;
            this.readyState = this.CONNECTING;

            if (this.url != address) {
                return new WindowWebSocket(url, protocols);
            }

            const client = {};

            client.send = (data) => {
                queueMicrotask(() => {if (this.onmessage) this.onmessage({ data: data })});
            };

            this.send = (data) => {
                if (this.readyState !== 1) throw new Error("WebSocket is not open");
                queueMicrotask(() => {if (client.onmessage) client.onmessage({ data: data })});
            };

            this.close = (code = 1000, reason = "") => {
                this.readyState = this.CLOSED;
            };

            queueMicrotask(() => {
                if (!onconnection(client)) return;
                this.readyState = this.OPEN;
                if (this.onopen) this.onopen();
            });
        }
    }

    window.WebSocket = EmscriptenMockWebSocket;
}

export function Start(address, stackWorkerFile, stackImage, readyCallback) {
    emscriptenMockWebSocket(address, (client) => {
        if (curSocket != null) {
            console.log("duplicated");
            return false;
        }
        curSocket = client;
        sockAccept();
        client.onmessage = (event) => {
            if (!accepted) {
                return;
            }
            eventQueue.push(new Uint8Array(event.data));
            sockSend(); // pass data from qemu to c2w-net-proxy.wasm
        }
        return true;
    });

    stackWorker = new Worker(stackWorkerFile);

    let conn = createStack(stackWorker, stackImage, readyCallback);
    registerConnBuffer(conn.toNet, conn.fromNet);
    registerMetaBuffer(conn.metaFromNet);
}

var toNetCtrl;
var toNetBegin;
var toNetEnd;
var toNetNotify;
var toNetData;
var fromNetCtrl;
var fromNetBegin;
var fromNetEnd;
var fromNetData;
function registerConnBuffer(to, from) {
    toNetCtrl = new Int32Array(to, 0, 1);
    toNetBegin = new Int32Array(to, 4, 1);
    toNetEnd = new Int32Array(to, 8, 1);
    toNetNotify = new Int32Array(to, 12, 1);
    toNetData = new Uint8Array(to, 16);
    fromNetCtrl = new Int32Array(from, 0, 1);
    fromNetBegin = new Int32Array(from, 4, 1);
    fromNetEnd = new Int32Array(from, 8, 1);
    fromNetData = new Uint8Array(from, 12);
}

var metaFromNetCtrl;
var metaFromNetBegin;
var metaFromNetEnd;
var metaFromNetStatus;
var metaFromNetData;
function registerMetaBuffer(meta) {
    metaFromNetCtrl = new Int32Array(meta, 0, 1);
    metaFromNetBegin = new Int32Array(meta, 4, 1);
    metaFromNetEnd = new Int32Array(meta, 8, 1);
    metaFromNetStatus = new Int32Array(meta, 12, 1);
    metaFromNetData = new Uint8Array(meta, 16);
}

function sockAccept(){
    accepted = true;
    return true;
}

function sockSend(){
    if (Atomics.compareExchange(toNetCtrl, 0, 0, 1) != 0) {
        setTimeout(() => {
            sockSend();
        }, 0);
        return;
    }
    const data = eventQueue.shift();
    let begin = toNetBegin[0]; //inclusive
    let end = toNetEnd[0]; //exclusive
    var len;
    var round;
    if (end >= begin) {
        len = toNetData.byteLength - end;
        round = begin;
    } else {
        len = begin - end;
        round = 0;
    }
    if ((len + round) < data.length) {
        // buffer is full; drop packets
        // TODO: preserve this
        console.log("FIXME: buffer full; dropping packets");
    } else {
        if (len > 0) {
            if (len > data.length) {
                len = data.length
            }
            toNetData.set(data.subarray(0, len), end);
            toNetEnd[0] = end + len;
        }
        if ((round > 0) && (data.length > len)) {
            if (round > data.length - len) {
                round = data.length - len
            }
            toNetData.set(data.subarray(len, len + round), 0);
            toNetEnd[0] = round;
        }
    }
    if (Atomics.compareExchange(toNetCtrl, 0, 1, 0) != 1) {
        console.log("UNEXPECTED STATUS");
    }
    Atomics.notify(toNetCtrl, 0, 1);

    Atomics.store(toNetNotify, 0, 1);
    Atomics.notify(toNetNotify, 0);
    return 0;
}

function sockRecvWS(targetLen){
    if (!accepted) {
        return -1;
    }

    if (Atomics.compareExchange(fromNetCtrl, 0, 0, 1) != 0) {
        sockRecvWS(targetLen);
        return;
    }
    let begin = fromNetBegin[0]; //inclusive
    let end = fromNetEnd[0]; //exclusive
    var len;
    var round;
    if (end >= begin) {
        len = end - begin;
        round = 0;
    } else {
        len = fromNetData.byteLength - begin;
        round = end;
    }
    if (targetLen < len) {
        len = targetLen;
        round = 0;
    } else if (targetLen < len + round) {
        round = targetLen - len;
    }
    let targetBuf = new Uint8Array(len + round);
    if (len > 0) {
        targetBuf.set(fromNetData.subarray(begin, begin + len), 0);
        fromNetBegin[0] = begin + len;
    }
    if (round > 0) {
        targetBuf.set(fromNetData.subarray(0, round), len);
        fromNetBegin[0] = round;
    }

    curSocket.send(targetBuf);
    
    if (Atomics.compareExchange(fromNetCtrl, 0, 1, 0) != 1) {
        console.log("UNEXPECTED STATUS");
    }
    Atomics.notify(fromNetCtrl, 0, 1);

    return (len + round);
}

export function RecvCert(){
    var buf = new Uint8Array(0);
    return new Promise((resolve, reject) => {
        function getCert(){
            var done = false;
            for(;;) {
                if (Atomics.compareExchange(metaFromNetCtrl, 0, 0, 1) == 0) {
                    break;
                }
                Atomics.wait(metaFromNetCtrl, 0, 1);
            }
            let end = metaFromNetEnd[0]; //exclusive
            if (end > 0) {
                buf = appendData(buf, metaFromNetData.slice(0, end));
                metaFromNetEnd[0] = 0;
            }
            if (metaFromNetStatus[0] == 1) {
                done = true;
            }
            if (Atomics.compareExchange(metaFromNetCtrl, 0, 1, 0) != 1) {
                console.log("UNEXPECTED STATUS");
            }
            Atomics.notify(metaFromNetCtrl, 0, 1);

            if (done) {
                resolve(buf); // EOF
            } else {
                setTimeout(getCert, 0);
                return;
            }
        }
        getCert();
    });
}

function createStack(stackWorker, stackWasmURL, readyCallback) {
    var proxyShared = new SharedArrayBuffer(12 + 1024 * 1024);

    var toShared = new SharedArrayBuffer(1024 * 1024);
    var fromShared = new SharedArrayBuffer(1024 * 1024);
    var toNetCtrl = new Int32Array(toShared, 0, 1);
    var toNetBegin = new Int32Array(toShared, 4, 1);
    var toNetEnd = new Int32Array(toShared, 8, 1);
    var toNetNotify = new Int32Array(toShared, 12, 1);
    var toNetData = new Uint8Array(toShared, 16);
    var fromNetCtrl = new Int32Array(fromShared, 0, 1);
    var fromNetBegin = new Int32Array(fromShared, 4, 1);
    var fromNetEnd = new Int32Array(fromShared, 8, 1);
    var fromNetData = new Uint8Array(fromShared, 12);
    toNetCtrl[0] = 0;
    toNetBegin[0] = 0;
    toNetEnd[0] = 0;
    toNetNotify[0] = 0;
    fromNetCtrl[0] = 0;
    fromNetBegin[0] = 0;
    fromNetEnd[0] = 0;

    var metaFromShared = new SharedArrayBuffer(4096);
    var metaFromNetCtrl = new Int32Array(metaFromShared, 0, 1);
    var metaFromNetBegin = new Int32Array(metaFromShared, 4, 1);
    var metaFromNetEnd = new Int32Array(metaFromShared, 8, 1);
    var metaFromNetStatus = new Int32Array(metaFromShared, 12, 1);
    var metaFromNetData = new Uint8Array(metaFromShared, 16);
    metaFromNetCtrl[0] = 0;
    metaFromNetBegin[0] = 0;
    metaFromNetEnd[0] = 0;
    metaFromNetStatus[0] = 0;
    
    var certbuf = {
        buf: new Uint8Array(0),
        readyCallback: readyCallback
    }
    stackWorker.onmessage = connect("proxy", proxyShared, toShared, certbuf);
    stackWorker.postMessage({type: "init", buf: proxyShared, toBuf: toShared, fromBuf: fromShared, stackWasmURL: stackWasmURL, metaFromBuf: metaFromShared});
    return {
        toNet: toShared,
        fromNet: fromShared,
        metaFromNet: metaFromShared
    };
}

function connect(name, shared, toNet, certbuf) {
    var streamCtrl = new Int32Array(shared, 0, 1);
    var streamStatus = new Int32Array(shared, 4, 1);
    var streamLen = new Int32Array(shared, 8, 1);
    var streamData = new Uint8Array(shared, 12);
    var httpConnections = [];
    var curID = 0;
    var maxID = 0x7FFFFFFF; // storable in streamStatus(signed 32bits)
    function getID() {
        var startID = curID;
        while (true) {
            if (httpConnections[curID] == undefined) {
                return curID;
            }
            if (curID >= maxID) {
                curID = 0;
            } else {
                curID++;
            }
            if (curID == startID) {
                return -1; // exhausted
            }
        }
        return curID;
    }
    function serveData(data, len) {
        var length = len;
        if (length > streamData.byteLength)
            length = streamData.byteLength;
        if (length > data.byteLength)
            length = data.byteLength
        var buf = data.subarray(0, length);
        var remain = data.subarray(length, data.byteLength);
        streamLen[0] = buf.byteLength;
        streamData.set(buf, 0);
        return remain;
    }
    function serveDataOffset(data, off, len) {
        var length = len;
        if (length > streamData.byteLength)
            length = streamData.byteLength;
        if (off + length > data.byteLength)
            length = data.byteLength - off
        var buf = data.subarray(off, off + length);
        streamLen[0] = buf.byteLength;
        streamData.set(buf, 0);
        return;
    }
    var timeoutHandler = null;
    var timeoutDeadlineMilli = null;
    let toNetCtrl = new Int32Array(toNet, 0, 1);
    let toNetBegin = new Int32Array(toNet, 4, 1);
    let toNetEnd = new Int32Array(toNet, 8, 1);
    let toNetNotify = new Int32Array(toNet, 12, 1);
    let toNetData = new Uint8Array(toNet, 16);
    function isBufReadable() {
        let begin = toNetBegin[0]; //inclusive
        let end = toNetEnd[0]; //exclusive
        var len;
        var round;
        if (end >= begin) {
            len = end - begin;
            round = 0;
        } else {
            len = toNetData.byteLength - begin;
            round = end;
        }
        if ((len + round) > 0) {
            // ready
            return true;
        } else {
            // not ready
            return false;
        }
    }
    return function(msg){
        const req_ = msg.data;
        if (typeof req_ == "object" && req_.type) {
            switch (req_.type) {
                case "recv-is-readable-cancel":
                    if (timeoutHandler) {
                        clearTimeout(timeoutHandler);
                        timeoutHandler = null;
                    }
                    break;
                case "recv-is-readable":
                    if (isBufReadable()) {
                        streamStatus[0] = 1; // ready for reading
                        Atomics.store(toNetNotify, 0, 1);
                        Atomics.notify(toNetNotify, 0);
                    } else {
                        if ((req_.timeout != undefined) && (req_.timeout > 0)) {
                            if (timeoutHandler) {
                                clearTimeout(timeoutHandler);
                                timeoutHandler = null;
                                timeoutDeadlineMilli = null;
                            }
                            timeoutDeadlineMilli = Date.now() + req_.timeout * 1000;
                            function pollBuf() {
                                timeoutHandler = setInterval(() => {
                                    if (isBufReadable()) {
                                        streamStatus[0] = 1; // ready for reading
                                        Atomics.store(toNetNotify, 0, 1);
                                    } else {
                                        if (Date.now() < timeoutDeadlineMilli) {
                                            return; // try again
                                        }
                                        streamStatus[0] = 0; // timeout
                                        Atomics.store(toNetNotify, 0, -1);
                                    }
                                    Atomics.notify(toNetNotify, 0);
                                    clearTimeout(timeoutHandler);
                                    timeoutHandler = null;
                                }, 0.01);
                            }
                            pollBuf();
                        } else {
                            streamStatus[0] = 0; // timeout
                            Atomics.store(toNetNotify, 0, -1);
                            Atomics.notify(toNetNotify, 0);
                        }
                    }
                    break;
                case "notify-send-from-net":
                    sockRecvWS(req_.len);
                    break;
                case "http_send":
                    var reqObj = JSON.parse(new TextDecoder().decode(req_.req));
                    reqObj.mode = "cors";
                    reqObj.credentials = "omit";
                    if (reqObj.headers && reqObj.headers["User-Agent"] != "") {
                        delete reqObj.headers["User-Agent"]; // Browser will add its own value.
                    }
                    var reqID = getID();
                    if (reqID < 0) {
                        console.log(name + ":" + "failed to get id");
                        streamStatus[0] = -1;
                        break;
                    }
                    var connObj = {
                        address: new TextDecoder().decode(req_.address),
                        request: reqObj,
                        requestSent: false,
                        reqBodybuf: new Uint8Array(0),
                        reqBodyEOF: false,

                        response: null,
                        done: null,
                        respBodybuf: null,
                    };
                    httpConnections[reqID] = connObj;
                    streamStatus[0] = reqID;
                    break;
                case "http_writebody":
                    var s;
                    if (httpConnections[req_.id] == undefined) {
                        s = newUint8Array(0);
                    } else {
                        s = httpConnections[req_.id].reqBodybuf;
                    }
                    httpConnections[req_.id].reqBodybuf = appendData(s, req_.body)
                    httpConnections[req_.id].reqBodyEOF = req_.isEOF;
                    streamStatus[0] = 0;
                    if (req_.isEOF && !httpConnections[req_.id].requestSent) {
                        httpConnections[req_.id].requestSent = true;
                        var connObj = httpConnections[req_.id];
                        if ((connObj.request.method != "HEAD") && (connObj.request.method != "GET")) {
                            connObj.request.body = connObj.reqBodybuf;
                        }
                        fetch(connObj.address, connObj.request).then((resp) => {
                            connObj.done = false;
                            connObj.respBodybuf = new Uint8Array(0);
                            if (resp.ok) {
                                resp.arrayBuffer().then((data) => {
                                    var headers = {};
                                    for (const key of resp.headers.keys()) {
                                        if (data.byteLength > 0) {
                                            if (key == "content-encoding") {
                                                continue;
                                            }
                                            if (key == "content-length") {
                                                headers[key] = data.byteLength.toString();
                                                continue;
                                            }
                                        }
                                        headers[key] = resp.headers.get(key);
                                    }
                                    connObj.response = new TextEncoder().encode(JSON.stringify({
                                        bodyUsed: resp.bodyUsed,
                                        headers: headers,
                                        redirected: resp.redirected,
                                        status: resp.status,
                                        statusText: resp.statusText,
                                        type: resp.type,
                                        url: resp.url
                                    }));
                                    connObj.respBodybuf = new Uint8Array(data);
                                    connObj.done = true;
                                }).catch((error) => {
                                    var headers = {};
                                    for (const key of resp.headers.keys()) {
                                        headers[key] = resp.headers.get(key);
                                    }
                                    connObj.response = new TextEncoder().encode(JSON.stringify({
                                        bodyUsed: resp.bodyUsed,
                                        headers: headers,
                                        redirected: resp.redirected,
                                        status: resp.status,
                                        statusText: resp.statusText,
                                        type: resp.type,
                                        url: resp.url
                                    }));
                                    connObj.respBodybuf = new Uint8Array(0);
                                    connObj.done = true;
                                    console.log("failed to fetch body: " + error);
                                });
                            } else {
                                var headers = {};
                                for (const key of resp.headers.keys()) {
                                    headers[key] = resp.headers.get(key);
                                }
                                connObj.response = new TextEncoder().encode(JSON.stringify({
                                    bodyUsed: resp.bodyUsed,
                                    headers: headers,
                                    redirected: resp.redirected,
                                    status: resp.status,
                                    statusText: resp.statusText,
                                    type: resp.type,
                                    url: resp.url
                                }));
                                connObj.done = true;
                            }
                        }).catch((error) => {
                            connObj.response = new TextEncoder().encode(JSON.stringify({
                                status: 503,
                                statusText: "Service Unavailable",
                            }))
                            connObj.respBodybuf = new Uint8Array(0);
                            connObj.done = true;
                        });
                    }
                    break;
                case "http_isreadable":
                    if ((httpConnections[req_.id] != undefined) && (httpConnections[req_.id].response != null)) {
                        streamData[0] = 1; // ready for reading
                    } else {
                        streamData[0] = 0; // nothing to read
                    }
                    streamStatus[0] = 0;
                    break;
                case "http_recv":
                    if ((httpConnections[req_.id] == undefined) || (httpConnections[req_.id].response == null)) {
                        console.log(name + ":" + "response is not available");
                        streamStatus[0] = -1;
                        break;
                    }
                    httpConnections[req_.id].response = serveData(httpConnections[req_.id].response, req_.len);
                    streamStatus[0] = 0;
                    if (httpConnections[req_.id].response.byteLength == 0) {
                        streamStatus[0] = 1; // isEOF
                    }
                    break;
                case "http_readbody":
                    if ((httpConnections[req_.id] == undefined) || (httpConnections[req_.id].response == null)) {
                        console.log(name + ":" + "response body is not available");
                        streamStatus[0] = -1;
                        break;
                    }
                    httpConnections[req_.id].respBodybuf = serveData(httpConnections[req_.id].respBodybuf, req_.len);
                    streamStatus[0] = 0;
                    if ((httpConnections[req_.id].done) && (httpConnections[req_.id].respBodybuf.byteLength == 0)) {
                        streamStatus[0] = 1;
                        delete httpConnections[req_.id]; // connection done
                    }
                    break;
                case "send_cert":
                    certbuf.buf = appendData(certbuf.buf, req_.buf);
                    certbuf.readyCallback(certbuf.buf);
                    streamStatus[0] = 0;
                    break;
                default:
                    console.log(name + ":" + "unknown request: " +  req_.type)
                    return;
            }
            Atomics.store(streamCtrl, 0, 1);
            Atomics.notify(streamCtrl, 0);
        } else {
            console.log("UNKNOWN MSG " + msg);
        }
    }
}

var decompressID = 0;
var decompressors = {};

function appendData(data1, data2) {
    let buf2 = new Uint8Array(data1.byteLength + data2.byteLength);
    buf2.set(new Uint8Array(data1), 0);
    buf2.set(new Uint8Array(data2), data1.byteLength);
    return buf2;
}
