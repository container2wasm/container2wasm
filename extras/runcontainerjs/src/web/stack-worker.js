import { WASI } from "@bjorn3/browser_wasi_shim";
import * as wasitype from "@bjorn3/browser_wasi_shim";
import { Event, EventType, Subscription, SubscriptionClock, SubscriptionFdReadWrite, SubscriptionU, wasiHackSocket } from './wasi-util';

onmessage = (msg) => {
    var info = serveIfInitMsg(msg);
    if (info == null) {
        return;
    }
    var fds = [
        undefined, // 0: stdin
        undefined, // 1: stdout
        undefined, // 2: stderr
        undefined, // 3: receive certificates
        undefined, // 4: socket listenfd
        undefined, // 5: accepted socket fd (multi-connection is unsupported)
        // 6...: used by wasi shim
    ];
    var certfd = 3;
    var listenfd = 4;
    var args = ['arg0', '--certfd='+certfd, '--net-listenfd='+listenfd, '--image-addr='+info.imageAddr];
    var env = [];
    var wasi = new WASI(args, env, fds);
    wasiHack(wasi, certfd, 5);
    wasiHackSocket(wasi, listenfd, 5, sockAccept, sockSend, sockRecv);
    fetch(info.mounterWasmURL).then((resp) => {
        resp['blob']().then((blob) => {
            const ds = new DecompressionStream("gzip")
            new Response(blob.stream().pipeThrough(ds))['arrayBuffer']().then((wasm) => {
                WebAssembly.instantiate(wasm, {
                    "wasi_snapshot_preview1": wasi.wasiImport,
                    "env": envHack(wasi),
                }).then((inst) => {
                    wasi.start(inst.instance);
                });

            });
        });
    });
};

// definition from wasi-libc https://github.com/WebAssembly/wasi-libc/blob/wasi-sdk-19/expected/wasm32-wasi/predefined-macros.txt
const ERRNO_INVAL = 28;
const ERRNO_AGAIN= 6;

function wasiHack(wasi, certfd, connfd) {
    var certbuf = new Uint8Array(0);
    var _fd_close = wasi.wasiImport.fd_close;
    wasi.wasiImport.fd_close = (fd) => {
        if (fd == certfd) {
            sendCert(certbuf);
            return 0;
        }
        return _fd_close.apply(wasi.wasiImport, [fd]);
    }
    var _fd_fdstat_get = wasi.wasiImport.fd_fdstat_get;
    wasi.wasiImport.fd_fdstat_get = (fd, fdstat_ptr) => {
        if (fd == certfd) {
            return 0;
        }
        return _fd_fdstat_get.apply(wasi.wasiImport, [fd, fdstat_ptr]);
    }
    wasi.wasiImport.fd_fdstat_set_flags = (fd, fdflags) => {
        // TODO
        return 0;
    }
    var stdoutBuf = "";
    var stderrBuf = "";
    function flushLog(msg) {
        if (msg.endsWith('\n') || msg.endsWith('\r\n')) {
            postMessage({
                type: "log",
                msg: msg.trim(),
            });
            return true;
        }
        return false;
    }
    var _fd_write = wasi.wasiImport.fd_write;
    wasi.wasiImport.fd_write = (fd, iovs_ptr, iovs_len, nwritten_ptr) => {
        if ((fd == 1) || (fd == 2) || (fd == certfd)) {
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            var buffer8 = new Uint8Array(wasi.inst.exports.memory.buffer);
            var iovecs = wasitype.wasi.Ciovec.read_bytes_array(buffer, iovs_ptr, iovs_len);
            var wtotal = 0
            for (var i = 0; i < iovecs.length; i++) {
                var iovec = iovecs[i];
                var buf = buffer8.slice(iovec.buf, iovec.buf + iovec.buf_len);
                if (buf.length == 0) {
                    continue;
                }
                const msg = new TextDecoder().decode(buf);
                if ((fd == 1) || (fd == 2)) {
                    console.debug(msg);
                    if (fd == 1) {
                        stdoutBuf += msg;
                        if (flushLog(stdoutBuf)) {
                            stdoutBuf = "";
                        }
                    } else {
                        stderrBuf += msg;
                        if (flushLog(stderrBuf)) {
                            stderrBuf = "";
                        }
                    }
                }
                if (fd == certfd) {
                    certbuf = appendData(certbuf, buf);
                }
                wtotal += buf.length;
            }
            buffer.setUint32(nwritten_ptr, wtotal, true);
            return 0;
        }
        console.log("fd_write: unknown fd " + fd);
        return _fd_write.apply(wasi.wasiImport, [fd, iovs_ptr, iovs_len, nwritten_ptr]);
    }
    wasi.wasiImport.poll_oneoff = (in_ptr, out_ptr, nsubscriptions, nevents_ptr) => {
        if (nsubscriptions == 0) {
            return ERRNO_INVAL;
        }
        let buffer = new DataView(wasi.inst.exports.memory.buffer);
        let in_ = Subscription.read_bytes_array(buffer, in_ptr, nsubscriptions);
        let isReadPollStdin = false;
        let isReadPollConn = false;
        let isClockPoll = false;
        let pollSubStdin;
        let pollSubConn;
        let clockSub;
        let timeout = Number.MAX_VALUE;
        for (let sub of in_) {
            if (sub.u.tag.variant == "fd_read") {
                if ((sub.u.data.fd != 0) && (sub.u.data.fd != connfd)) {
                    return ERRNO_INVAL; // only fd=0 and connfd is supported as of now (FIXME)
                }
                if (sub.u.data.fd == 0) {
                    isReadPollStdin = true;
                    pollSubStdin = sub;
                } else {
                    isReadPollConn = true;
                    pollSubConn = sub;
                }
            } else if (sub.u.tag.variant == "clock") {
                if (sub.u.data.timeout < timeout) {
                    timeout = sub.u.data.timeout
                    isClockPoll = true;
                    clockSub = sub;
                }
            } else {
                return ERRNO_INVAL; // FIXME
            }
        }
        if (!isClockPoll) {
            timeout = 0;
        }
        let events = [];
        if (isReadPollStdin || isReadPollConn || isClockPoll) {
            var sockreadable = sockWaitForReadable(timeout / 1000000000);
            if (isReadPollConn) {
                if (sockreadable == errStatus) {
                    return ERRNO_INVAL;
                } else if (sockreadable == true) {
                    let event = new Event();
                    event.userdata = pollSubConn.userdata;
                    event.error = 0;
                    event.type = new EventType("fd_read");
                    events.push(event);
                }
            }
            if (isClockPoll) {
                let event = new Event();
                event.userdata = clockSub.userdata;
                event.error = 0;
                event.type = new EventType("clock");
                events.push(event);
            }
        }
        var len = events.length;
        Event.write_bytes_array(buffer, out_ptr, events);
        buffer.setUint32(nevents_ptr, len, true);
        return 0;
    }
}

function envHack(wasi){
    return {
        http_send: function(addressP, addresslen, reqP, reqlen, idP){
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            var address = new Uint8Array(wasi.inst.exports.memory.buffer, addressP, addresslen);
            var req = new Uint8Array(wasi.inst.exports.memory.buffer, reqP, reqlen);
            streamCtrl[0] = 0;
            postMessage({
                type: "http_send",
                address: address.slice(0, address.length),
                req: req.slice(0, req.length),
            });
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            var id = streamStatus[0];
            buffer.setUint32(idP, id, true);
            return 0;
        },
        http_writebody: function(id, bodyP, bodylen, nwrittenP, isEOF){
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            var body = new Uint8Array(wasi.inst.exports.memory.buffer, bodyP, bodylen);
            streamCtrl[0] = 0;
            postMessage({
                type: "http_writebody",
                id: id,
                body: body.slice(0, body.length),
                isEOF: isEOF,
            });
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            buffer.setUint32(nwrittenP, bodylen, true);
            return 0;
        },
        http_isreadable: function(id, isOKP){
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            streamCtrl[0] = 0;
            postMessage({type: "http_isreadable", id: id});
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            var readable = 0;
            if (streamData[0] == 1) {
                readable = 1;
            }
            buffer.setUint32(isOKP, readable, true);
            return 0;
        },
        http_recv: function(id, respP, bufsize, respsizeP, isEOFP){
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            var buffer8 = new Uint8Array(wasi.inst.exports.memory.buffer);

            streamCtrl[0] = 0;
            postMessage({type: "http_recv", id: id, len: bufsize});
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            var ddlen = streamLen[0];
            var resp = streamData.subarray(0, ddlen);
            buffer8.set(resp, respP);
            buffer.setUint32(respsizeP, ddlen, true);
            if (streamStatus[0] == 1) {
                buffer.setUint32(isEOFP, 1, true);
            } else {
                buffer.setUint32(isEOFP, 0, true);
            }
            return 0;
        },
        http_readbody: function(id, bodyP, bufsize, bodysizeP, isEOFP){
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            var buffer8 = new Uint8Array(wasi.inst.exports.memory.buffer);

            streamCtrl[0] = 0;
            postMessage({type: "http_readbody", id: id, len: bufsize});
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            var ddlen = streamLen[0];
            var body = streamData.subarray(0, ddlen);
            buffer8.set(body, bodyP);
            buffer.setUint32(bodysizeP, ddlen, true);
            if (streamStatus[0] == 1) {
                buffer.setUint32(isEOFP, 1, true);
            } else {
                buffer.setUint32(isEOFP, 0, true);
            }
            return 0;
        },
        decompress_init: function(idP) {
            if (idP < 0) {
                idP = idP >>> 0;
            }
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            var buffer8 = new Uint8Array(wasi.inst.exports.memory.buffer);

            streamCtrl[0] = 0;
            postMessage({
                type: "decompress_init",
            });
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            var id = streamStatus[0];
            buffer.setUint32(idP, id, true);
            return 0;
        },
        decompress_write: function(id, bufP, buflen, isEOF) {
            if (bufP < 0) {
                bufP = bufP >>> 0;
            }
            var buf = new Uint8Array(wasi.inst.exports.memory.buffer, bufP, buflen);
            streamCtrl[0] = 0;
            postMessage({
                type: "decompress_write",
                id: id,
                chunk: buf.slice(0, buf.length),
                isEOF: isEOF,
            });
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            return 0;
        },
        decompress_read: function(id, bufP, buflen, recvLenP, isEOFP) {
            if (bufP < 0) {
                bufP = bufP >>> 0;
            }
            if (recvLenP < 0) {
                recvLenP = recvLenP >>> 0;
            }
            if (isEOFP < 0) {
                isEOFP = isEOFP >>> 0;
            }
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            var buffer8 = new Uint8Array(wasi.inst.exports.memory.buffer);

            streamCtrl[0] = 0;
            postMessage({type: "decompress_read", id: id, len: buflen});
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            var ddlen = streamLen[0];
            var res = streamData.subarray(0, ddlen);
            buffer8.set(res, bufP);
            buffer.setUint32(recvLenP, ddlen, true);
            if (streamStatus[0] == 1) {
                buffer.setUint32(isEOFP, 1, true);
            } else {
                buffer.setUint32(isEOFP, 0, true);
            }
            return 0;
        },
        layer_request: function (addressP, addresslen, digestP, digestlen, isGzipN, idP) {
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            var address = new Uint8Array(wasi.inst.exports.memory.buffer, addressP, addresslen);
            var digest = new Uint8Array(wasi.inst.exports.memory.buffer, digestP, digestlen);
            streamCtrl[0] = 0;
            postMessage({
                type: "layer_request",
                address: address.slice(0, address.length),
                digest: digest,
                isGzipN: isGzipN,
            });
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            var id = streamStatus[0];
            buffer.setUint32(idP, id, true);
            return 0;
        },
        layer_isreadable: function(id, isOKP, sizeP) {
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            streamCtrl[0] = 0;
            postMessage({type: "layer_isreadable", id: id});
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            var readable = 0;
            if (streamData[0] == 1) {
                readable = 1;
            }
            buffer.setUint32(isOKP, readable, true);
            var size = streamStatus[0];
            buffer.setUint32(sizeP, size, true);
            return 0;
        },
        layer_readat: function(id, respP, offset, len, respsizeP) {
            var buffer = new DataView(wasi.inst.exports.memory.buffer);
            var buffer8 = new Uint8Array(wasi.inst.exports.memory.buffer);

            streamCtrl[0] = 0;
            postMessage({
                type: "layer_readat",
                id: id,
                offset: offset,
                len: len,
            });
            Atomics.wait(streamCtrl, 0, 0);
            if (streamStatus[0] < 0) {
                return ERRNO_INVAL;
            }
            var ddlen = streamLen[0];
            if (ddlen > len) {
                ddlen = len
            }
            var body = streamData.subarray(0, ddlen);
            buffer8.set(body, respP);
            buffer.setUint32(respsizeP, ddlen, true);
            return 0;
        },
    };
}

var streamCtrl;
var streamStatus;
var streamLen;
var streamData;
function registerSocketBuffer(shared){
    streamCtrl = new Int32Array(shared, 0, 1);
    streamStatus = new Int32Array(shared, 4, 1);
    streamLen = new Int32Array(shared, 8, 1);
    streamData = new Uint8Array(shared, 12);
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

function serveIfInitMsg(msg) {
    const req_ = msg.data;
    if (typeof req_ == "object"){
        if (req_.type == "init") {
            if (req_.buf) {
                var shared = req_.buf;
                registerSocketBuffer(shared);
                registerConnBuffer(req_.toBuf, req_.fromBuf);
                registerMetaBuffer(req_.metaFromBuf);
            }
            return {
                mounterWasmURL: req_.mounterWasmURL,
                imageAddr: req_.imageAddr
            };
        }
    }

    return null;
}

const errStatus = {
    val: 0,
};

var accepted = false;
function sockAccept(){
    accepted = true;
    return true;
}

function sockSend(data){
    if (!accepted) {
        return -1;
    }

    for(;;) {
        if (Atomics.compareExchange(fromNetCtrl, 0, 0, 1) == 0) {
            break;
        }
        Atomics.wait(fromNetCtrl, 0, 1);
    }
    let begin = fromNetBegin[0]; //inclusive
    let end = fromNetEnd[0]; //exclusive
    var len;
    var round;
    if (end >= begin) {
        len = fromNetData.byteLength - end;
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
            fromNetData.set(data.subarray(0, len), end);
            fromNetEnd[0] = end + len;
        }
        if ((round > 0) && (data.length > len)) {
            if (round > data.length - len) {
                round = data.length - len
            }
            fromNetData.set(data.subarray(len, len + round), 0);
            fromNetEnd[0] = round;
        }
    }
    if (Atomics.compareExchange(fromNetCtrl, 0, 1, 0) != 1) {
        console.log("UNEXPECTED STATUS");
    }
    Atomics.notify(fromNetCtrl, 0, 1);

    // notify data is sent from stack
    streamCtrl[0] = 0;
    postMessage({type: "notify-send-from-net", len: data.length});
    Atomics.wait(streamCtrl, 0, 0);

    return 0;
}

function sockRecv(targetBuf, targetOffset, targetLen){
    if (!accepted) {
        return -1;
    }

    for(;;) {
        if (Atomics.compareExchange(toNetCtrl, 0, 0, 1) == 0) {
            break;
        }
        Atomics.wait(toNetCtrl, 0, 1);
    }
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
    if (targetLen < len) {
        len = targetLen;
        round = 0;
    } else if (targetLen < len + round) {
        round = targetLen - len;
    }
    if (len > 0) {
        targetBuf.set(toNetData.subarray(begin, begin + len), targetOffset);
        toNetBegin[0] = begin + len;
    }
    if (round > 0) {
        targetBuf.set(toNetData.subarray(0, round), targetOffset + len);
        toNetBegin[0] = round;
    }
    if (Atomics.compareExchange(toNetCtrl, 0, 1, 0) != 1) {
        console.log("UNEXPECTED STATUS");
    }
    Atomics.notify(toNetCtrl, 0, 1);

    return (len + round);
}

function sockWaitForReadable(timeout){
    if (!accepted) {
        errStatus.val = -1;
        return errStatus;
    }

    for(;;) {
        if (Atomics.compareExchange(toNetCtrl, 0, 0, 1) == 0) {
            break;
        }
        Atomics.wait(toNetCtrl, 0, 1);
    }
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
    var ready;
    if ((len + round) > 0) {
        ready = true;
    } else {
        ready = false;
    }
    if (Atomics.compareExchange(toNetCtrl, 0, 1, 0) != 1) {
        console.log("UNEXPECTED STATUS");
    }
    Atomics.notify(toNetCtrl, 0, 1);

    if (ready) {
        return true;
    } else if (timeout == 0) {
        return false;
    }

    // buffer not ready; wait for readable.
    streamCtrl[0] = 0;
    Atomics.store(toNetNotify, 0, 0);
    postMessage({type: "recv-is-readable", timeout: timeout});
    Atomics.wait(streamCtrl, 0, 0);
    Atomics.wait(toNetNotify, 0, 0);
    var res = Atomics.load(toNetNotify, 0);

    streamCtrl[0] = 0;
    postMessage({type: "recv-is-readable-cancel"});
    Atomics.wait(streamCtrl, 0, 0);

    Atomics.store(toNetNotify, 0, 0);
    return res == 1;
}

function sendCert(data){
    streamCtrl[0] = 0;
    postMessage({type: "send_cert", buf: data});
    Atomics.wait(streamCtrl, 0, 0);
    if (streamStatus[0] < 0) {
        errStatus.val = streamStatus[0]
        return errStatus;
    }
}

function appendData(data1, data2) {
    let buf2 = new Uint8Array(data1.byteLength + data2.byteLength);
    buf2.set(new Uint8Array(data1), 0);
    buf2.set(new Uint8Array(data2), data1.byteLength);
    return buf2;
}
