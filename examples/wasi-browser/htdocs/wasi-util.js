import { wasi } from 'https://cdn.jsdelivr.net/npm/@bjorn3/browser_wasi_shim@0.3.0/+esm';

////////////////////////////////////////////////////////////
//
// event-related classes adopted from the on-going discussion
// towards poll_oneoff support in browser_wasi_sim project.
// Ref: https://github.com/bjorn3/browser_wasi_shim/issues/14#issuecomment-1450351935
//
////////////////////////////////////////////////////////////

export class EventType {
    /*:: variant: "clock" | "fd_read" | "fd_write"*/

    constructor(variant/*: "clock" | "fd_read" | "fd_write"*/) {
        this.variant = variant;
    }

    static from_u8(data/*: number*/)/*: EventType*/ {
        switch (data) {
            case wasi.EVENTTYPE_CLOCK:
                return new EventType("clock");
            case wasi.EVENTTYPE_FD_READ:
                return new EventType("fd_read");
            case wasi.EVENTTYPE_FD_WRITE:
                return new EventType("fd_write");
            default:
                throw "Invalid event type " + String(data);
        }
    }

    to_u8()/*: number*/ {
        switch (this.variant) {
            case "clock":
                return wasi.EVENTTYPE_CLOCK;
            case "fd_read":
                return wasi.EVENTTYPE_FD_READ;
            case "fd_write":
                return wasi.EVENTTYPE_FD_WRITE;
            default:
                throw "unreachable";
        }
    }
}

export class Event {
    /*:: userdata: UserData*/
    /*:: error: number*/
    /*:: type: EventType*/
    /*:: fd_readwrite: EventFdReadWrite | null*/

    write_bytes(view/*: DataView*/, ptr/*: number*/) {
        view.setBigUint64(ptr, this.userdata, true);
        view.setUint8(ptr + 8, this.error);
        view.setUint8(ptr + 9, 0);
        view.setUint8(ptr + 10, this.type.to_u8());
        // if (this.fd_readwrite) {
        //     this.fd_readwrite.write_bytes(view, ptr + 16);
        // }
    }

    static write_bytes_array(view/*: DataView*/, ptr/*: number*/, events/*: Array<Event>*/) {
        for (let i = 0; i < events.length; i++) {
            events[i].write_bytes(view, ptr + 32 * i);
        }
    }
}

export class SubscriptionClock {
    /*:: timeout: number*/

    static read_bytes(view/*: DataView*/, ptr/*: number*/)/*: SubscriptionFdReadWrite*/ {
        let self = new SubscriptionClock();
        self.timeout = Number(view.getBigUint64(ptr + 8, true));
        return self;
    }
}

export class SubscriptionFdReadWrite {
    /*:: fd: number*/

    static read_bytes(view/*: DataView*/, ptr/*: number*/)/*: SubscriptionFdReadWrite*/ {
        let self = new SubscriptionFdReadWrite();
        self.fd = view.getUint32(ptr, true);
        return self;
    }
}

export class SubscriptionU {
    /*:: tag: EventType */
    /*:: data: SubscriptionClock | SubscriptionFdReadWrite */

    static read_bytes(view/*: DataView*/, ptr/*: number*/)/*: SubscriptionU*/ {
        let self = new SubscriptionU();
        self.tag = EventType.from_u8(view.getUint8(ptr));
        switch (self.tag.variant) {
            case "clock":
                self.data = SubscriptionClock.read_bytes(view, ptr + 8);
                break;
            case "fd_read":
            case "fd_write":
                self.data = SubscriptionFdReadWrite.read_bytes(view, ptr + 8);
                break;
            default:
                throw "unreachable";
        }
        return self;
    }
}

export class Subscription {
    /*:: userdata: UserData */
    /*:: u: SubscriptionU */

    static read_bytes(view/*: DataView*/, ptr/*: number*/)/*: Subscription*/ {
        let subscription = new Subscription();
        subscription.userdata = view.getBigUint64(ptr, true);
        subscription.u = SubscriptionU.read_bytes(view, ptr + 8);
        return subscription;
    }

    static read_bytes_array(view/*: DataView*/, ptr/*: number*/, len/*: number*/)/*: Array<Subscription>*/ {
        let subscriptions = [];
        for (let i = 0; i < len; i++) {
            subscriptions.push(Subscription.read_bytes(view, ptr + 48 * i));
        }
        return subscriptions;
    }
}

