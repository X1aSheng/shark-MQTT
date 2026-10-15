"""Split broker/broker.go into files grouped by responsibility.

This is a pure code move: no logic changes, no edits to any function body. The
script only relocates whole top-level declarations (and the doc comments
immediately above them) into new files, which is the property that makes the
result reviewable -- `git diff --stat` afterwards must show nothing but
deletions from broker.go and additions elsewhere, with zero changed lines.

Why a script rather than by hand: broker.go is 3,876 lines with 84 top-level
declarations. Hand-cutting it invites a truncated comment or a lost function,
and the failure mode is a confusing compile error rather than a missing symbol.

Usage:
    python3 tools/split-broker.py            # dry run, prints the plan
    python3 tools/split-broker.py --apply    # perform the split
"""

import os
import re
import sys

BROKER = "broker/broker.go"

# Each entry: (output filename, package doc line, list of declaration names).
# A declaration lands in the first group that claims it, so the groups below
# must not overlap.
GROUPS = [
    ("conn_lifecycle.go",
     "Connection establishment: the handshake from accept through CONNECT to a\n// registered session, plus the rejection paths and the disconnect teardown.\n// Everything here runs on the connection's own goroutine.",
     [
         "connAckForConnectRejection", "HandleConnection", "rejectSessionOwnerMismatch",
         "rejectFlapping", "findEnhancedAuth", "enhancedAuthHandshake",
         "sendAuthPacket", "sendAuthPacketToClient", "disconnect",
         "setKeepAliveDeadline", "abnormalDisconnect", "gracefulDisconnect",
         "sendDisconnectBeforeClose", "sendTakenOverDisconnect",
         "readLoop", "decodeErrorReasonCode",
     ]),

    ("lifecycle.go",
     "Broker lifecycle: start, the internal cleanup loops, drain and stop.\n//\n// The ordering here is deliberate and load-bearing. Plugins start before\n// EventBrokerStarted is announced, because a plugin's Start may depend on the\n// broker being fully initialised; they stop after EventBrokerStopped, because a\n// plugin's Stop may still need to read session state.",
     [
         "Start", "startLoop", "rebuildRetainedExpirations", "sessionCleanupLoop",
         "cleanupExpiredSessions", "retainedCleanupLoop", "cleanupExpiredRetained",
         "sysStatusLoop", "publishSystemStatus", "publishSystem", "BeginShutdown",
         "notifyShuttingDown", "Stop", "DrainInflight", "drainInflight",
         "totalInflight", "teardownCtx",
     ]),

    ("pub_sub.go",
     "Publish and subscribe: inbound PUBLISH routing, retained messages,\n// SUBSCRIBE/UNSUBSCRIBE and outbound delivery to one client.",
     [
         "handlePublish", "rejectInboundOverBudget", "rejectMalformedTopic",
         "ackDiscardedPublish", "closeProtocolViolation", "closeProtocolViolationWithReason",
         "handleRetainedMessage", "handleSubscribe", "shouldDeliverRetained",
         "handleUnsubscribe", "messageExpiresAt", "deliverToClient",
         "unsubscribeTopics", "unsubscribeSessionTopics",
         "subAckFailureCode",
         # Outbound delivery: shared-subscription routing, offline queuing and
         # the QoS selection that decides how a message reaches one session.
         "deliverToSharedClient", "deliverWithQoS", "queueOfflineMessage",
         "nextMessageID", "adjustOfflineCount", "deliverQueuedMessages", "doDeliver",
         "flushBufferedOutbound", "persistBufferedOutbound", "isClientOnline",
         "connection", "routeSharedPublish", "deliverRetainedMessages", "writePacket",
     ]),

    ("connack.go",
     "CONNACK construction and delivery, including the MQTT 5.0 reason string\n// and properties.",
     [
         "sendConnAck", "buildConnAckProperties", "buildConnAck", "writeConnAck",
         "sendConnAckRaw",
     ]),

    ("qos_outbound.go",
     "Outbound QoS 1/2 flows: PUBACK/PUBREC/PUBREL/PUBCOMP handling,\n// inflight retransmission and Will publishing.",
     [
         "handlePubAck", "handlePubRec", "dropRejectedDelivery", "handlePubRel",
         "handlePubComp", "sendPubAck", "sendPubRel", "pubRelPacket",
         "buildRetransmitPublish", "restoreSessionInflight", "resendSessionInflight",
         "sendPubComp", "republish", "publishWill",
     ]),

    ("client_state.go",
     "Per-connection outbound state: the write queue and its goroutine, and\n// the classification of write failures.",
     [
         "classifyWriteError", "writeOrEnqueue", "writeLoop", "writeOne", "writeFinal",
     ]),

    ("admin.go",
     "Read-only introspection used by the management API, plus the two\n// explicit disconnect entry points an operator can call.",
     [
         "Clients", "Client", "clientInfo", "TopicStats", "DisconnectClient",
         "DisconnectAllClients",
     ]),
]

# Stays in broker.go: the Broker type itself, its constructor, the plugin
# dispatch helpers and the metric accessors. Everything that constitutes the
# type's shape rather than one of its behaviours.
KEEP_HINT = "Broker type, constructor and plugin dispatch"


def read(path):
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def split_top_level(src):
    """Yield (start, end, name, text) for each top-level declaration.

    A declaration starts at the first line of the comment block directly above
    it (so the doc comment travels with the function) and ends just before the
    next declaration or trailing content.
    """
    lines = src.split("\n")
    # Index of every line that begins a top-level declaration.
    decl = []
    for i, line in enumerate(lines):
        if re.match(r"^(func|type|var|const)\b", line):
            decl.append(i)

    out = []
    for n, start in enumerate(decl):
        # Walk back over the doc comment and any decorators.
        first = start
        while first > 0:
            prev = lines[first - 1].strip()
            if prev.startswith("//") or prev == "":
                # Only absorb a blank line if a comment follows it, otherwise we
                # would swallow the separator between two declarations.
                if prev == "":
                    nxt = lines[first] if first < len(lines) else ""
                    if not nxt.startswith("//"):
                        break
                first -= 1
                continue
            break
        end = decl[n + 1] if n + 1 < len(decl) else len(lines)
        name = decl_name(lines[start])
        out.append((first, end, name, "\n".join(lines[first:end])))
    return out, lines


def decl_name(line):
    m = re.match(r"^func\s+\((?:\w+\s+)?\*?(\w+)\)\s*(\w+)", line)
    if m:
        return m.group(2)
    m = re.match(r"^func\s+(\w+)", line)
    if m:
        return m.group(1)
    m = re.match(r"^type\s+(\w+)", line)
    if m:
        return m.group(1)
    m = re.match(r"^(?:var|const)\s+(\w+)", line)
    if m:
        return m.group(1)
    # Grouped var/const blocks.
    m = re.match(r"^(var|const)\s*\(", line)
    if m:
        return m.group(1) + "-block"
    return "<unknown:" + line[:40] + ">"


def main():
    apply = "--apply" in sys.argv
    src = read(BROKER)
    decls, lines = split_top_level(src)

    claimed = {}
    for filename, _, names in GROUPS:
        for n in names:
            if n in claimed:
                sys.exit("declaration %r claimed by both %s and %s"
                         % (n, claimed[n], filename))
            claimed[n] = filename

    assigned = {}
    for start, end, name, _ in decls:
        assigned[name] = claimed.get(name)

    unmapped = [n for n, f in assigned.items() if f is None]
    print("total declarations: %d" % len(decls))
    print("mapped to a group: %d" % (len(decls) - len(unmapped)))
    print("staying in broker.go: %s" % ", ".join(sorted(unmapped)))

    for filename, _, names in GROUPS:
        found = [n for n in names if n in assigned]
        print("  %-22s %3d declarations" % (filename, len(found)))

    # Everything up to the first declaration stays in broker.go.
    first_start = decls[0][0] if decls else len(lines)
    head = "\n".join(lines[:first_start])
    keep = [d for d in decls if assigned[d[2]] is None]

    if not apply:
        print("\ndry run; pass --apply to write")
        return

    # Copy rather than rename: os.rename fails on Windows when the destination
    # filesystem refuses the operation, and keeping the original around is what
    # lets us diff against it to prove the move was purely mechanical.
    orig = BROKER + ".orig"
    if os.path.exists(orig):
        os.remove(orig)
    with open(BROKER, encoding="utf-8") as src_fh, \
            open(orig, "w", encoding="utf-8", newline="\n") as dst_fh:
        dst_fh.write(src_fh.read())

    # Rebuild broker.go from the head plus everything unclaimed, in the original
    # order so the diff reads as a deletion rather than a reshuffle.
    parts = [head]
    for _, _, _, text in keep:
        parts.append(text.rstrip("\n"))
    with open(BROKER, "w", encoding="utf-8", newline="\n") as fh:
        fh.write("\n".join(parts).rstrip("\n") + "\n")

    for filename, doc, names in GROUPS:
        selected = [d for d in decls if assigned[d[2]] == filename]
        body = ["// Code in this file is part of the broker connection handling.",
                "// " + doc if False else "//"]
        header = "package broker\n\n" + _wrap_comment(doc) + "\n"
        chunks = []
        for _, _, _, text in selected:
            chunks.append(text.rstrip("\n"))
        with open(os.path.join("broker", filename), "w",
                  encoding="utf-8", newline="\n") as fh:
            fh.write(header + "\n".join(chunks) + "\n")
        print("wrote broker/%s (%d declarations)" % (filename, len(selected)))

    print("\nbroker.go.orig kept for diffing")


def _wrap_comment(doc):
    return "\n".join("// " + l for l in doc.split("\n"))


if __name__ == "__main__":
    main()