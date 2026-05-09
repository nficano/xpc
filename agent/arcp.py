# -*- coding: utf-8 -*-
"""xpc wire protocol -- Python 3.4-compatible mirror of internal/arcp.

This module is the on-VM half of the xpc wire protocol defined in
docs/PROTOCOL.md. Every public function MUST produce byte-identical output
to its Go counterpart (internal/arcp). A shared corpus
(tests/protocol_corpus.json) enforces this.

Compatibility note: this file targets Python 3.4 (which is the latest CPython
that runs on Windows XP). Do NOT use:
  * f-strings (3.6+)
  * 'async'/'await' as keywords (3.5+)
  * type hints in function signatures (3.5+)
  * dict/set comprehension scoping that 3.4 disallows
  * pathlib.Path nice-to-haves only on 3.6+
Stick to .format() / % formatting, return-type via docstrings, and stdlib only.
"""

from __future__ import absolute_import, print_function

import base64
import binascii
import datetime
import hashlib
import hmac
import json
import os
import struct

VERSION = "1.0"
MAX_ENVELOPE_BYTES = 50 * 1024 * 1024
AUTH_ALG = "HMAC-SHA256"
AUTH_KID = "v0"

PREFIX_MESSAGE = "msg"
PREFIX_SESSION = "sess"
PREFIX_JOB = "job"
PREFIX_STREAM = "str"
PREFIX_TRACE = "tr"
PREFIX_SPAN = "sp"

# Control message types.
TYPE_SESSION_OPEN = "session.open"
TYPE_SESSION_ACCEPTED = "session.accepted"
TYPE_SESSION_CLOSE = "session.close"
TYPE_PING = "ping"
TYPE_PONG = "pong"
TYPE_ACK = "ack"
TYPE_NACK = "nack"
TYPE_CANCEL = "cancel"
TYPE_PERMISSION_REQ = "permission.request"
TYPE_PERMISSION_GRANT = "permission.grant"
TYPE_PERMISSION_DENY = "permission.deny"

# Execution message types.
TYPE_TOOL_INVOKE = "tool.invoke"
TYPE_TOOL_RESULT = "tool.result"
TYPE_TOOL_ERROR = "tool.error"
TYPE_TOOLS_LIST = "tools.list"
TYPE_TOOLS_RESULT = "tools.list.result"
TYPE_JOB_ACCEPTED = "job.accepted"
TYPE_JOB_STARTED = "job.started"
TYPE_JOB_PROGRESS = "job.progress"
TYPE_JOB_COMPLETED = "job.completed"
TYPE_JOB_FAILED = "job.failed"
TYPE_JOB_CANCELLED = "job.cancelled"

# Streaming message types.
TYPE_STREAM_OPEN = "stream.open"
TYPE_STREAM_CHUNK = "stream.chunk"
TYPE_STREAM_CLOSE = "stream.close"
TYPE_STREAM_ERROR = "stream.error"

# Event message types.
TYPE_LOG = "log"

ALL_TYPES = (
    TYPE_SESSION_OPEN,
    TYPE_SESSION_ACCEPTED,
    TYPE_SESSION_CLOSE,
    TYPE_PING,
    TYPE_PONG,
    TYPE_ACK,
    TYPE_NACK,
    TYPE_CANCEL,
    TYPE_PERMISSION_REQ,
    TYPE_PERMISSION_GRANT,
    TYPE_PERMISSION_DENY,
    TYPE_TOOL_INVOKE,
    TYPE_TOOL_RESULT,
    TYPE_TOOL_ERROR,
    TYPE_TOOLS_LIST,
    TYPE_TOOLS_RESULT,
    TYPE_JOB_ACCEPTED,
    TYPE_JOB_STARTED,
    TYPE_JOB_PROGRESS,
    TYPE_JOB_COMPLETED,
    TYPE_JOB_FAILED,
    TYPE_JOB_CANCELLED,
    TYPE_STREAM_OPEN,
    TYPE_STREAM_CHUNK,
    TYPE_STREAM_CLOSE,
    TYPE_STREAM_ERROR,
    TYPE_LOG,
)

# Optional envelope fields (omitted when empty/None per docs/PROTOCOL.md).
_OPTIONAL_FIELDS = (
    "session_id",
    "job_id",
    "stream_id",
    "trace_id",
    "span_id",
    "parent_span_id",
    "correlation_id",
    "causation_id",
    "source",
    "target",
)

# Crockford-like base32 alphabet matching Go's base32 encoder
# (internal/arcp/ids.go). 32 chars, skipping i, l, o, u.
_CROCKFORD_ALPHABET = "0123456789abcdefghjkmnpqrstvwxyz"

# Translation table from RFC 4648 base32 ("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567")
# to our Crockford-like alphabet, position by position.
_CROCKFORD_TRANS = str.maketrans(
    "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567",
    _CROCKFORD_ALPHABET,
)


class ProtocolError(Exception):
    """Generic protocol error (auth failure, malformed envelope, etc.)."""


class TooLarge(ProtocolError):
    """Frame exceeds MAX_ENVELOPE_BYTES."""


def new_envelope(envelope_id, msg_type, timestamp):
    """Return a fresh envelope dict with required fields set.

    Args:
        envelope_id: e.g. 'msg_01HABCDEF...'
        msg_type: one of ALL_TYPES (or any string the agent recognizes)
        timestamp: RFC 3339 with microsecond precision and trailing Z

    Returns:
        dict ready to populate with payload and optional fields.
    """
    return {
        "arcp": VERSION,
        "id": envelope_id,
        "type": msg_type,
        "timestamp": timestamp,
        "auth": {"alg": AUTH_ALG, "kid": AUTH_KID, "sig": ""},
        "payload": {},
    }


def validate(envelope):
    """Raise ProtocolError if the envelope is missing required fields."""
    if not isinstance(envelope, dict):
        raise ProtocolError("envelope must be a dict")
    if envelope.get("arcp") != VERSION:
        raise ProtocolError("unsupported arcp version: {0!r}".format(envelope.get("arcp")))
    if not envelope.get("id"):
        raise ProtocolError("empty envelope id")
    if not envelope.get("type"):
        raise ProtocolError("empty envelope type")
    if not envelope.get("timestamp"):
        raise ProtocolError("empty timestamp")
    auth = envelope.get("auth") or {}
    if auth.get("alg") != AUTH_ALG:
        raise ProtocolError("unsupported auth alg: {0!r}".format(auth.get("alg")))
    if auth.get("kid") != AUTH_KID:
        raise ProtocolError("unsupported auth kid: {0!r}".format(auth.get("kid")))
    if envelope.get("payload") is None:
        raise ProtocolError("nil payload (use {} for empty)")


def canonical_marshal(value):
    """Marshal *value* to canonical JSON bytes.

    Sorted keys, no whitespace, no HTML escape, ``ensure_ascii=False``.
    The output is byte-identical to Go's ``internal/arcp`` ``canonicalMarshal``
    for the same input.
    """
    # sort_keys=True sorts at every nesting level.
    # separators=(',', ':') strips all whitespace.
    # ensure_ascii=False prevents \uXXXX escaping of non-ASCII characters,
    # matching Go's encoding/json default. For ASCII-only envelopes (the
    # typical case in xpc) this makes no difference; it matters for paths
    # with accented chars or stream content.
    return json.dumps(
        value,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
    ).encode("utf-8")


def _strip_empty_optionals(envelope):
    """Return a copy of *envelope* with empty optional fields removed.

    Mirrors Go's ``omitempty`` behavior on the fields listed in
    :data:`_OPTIONAL_FIELDS`.
    """
    out = {}
    for key, val in envelope.items():
        if key in _OPTIONAL_FIELDS and (val is None or val == ""):
            continue
        out[key] = val
    return out


def _deep_copy(value):
    """Stdlib-only deep copy avoiding the copy module's overhead."""
    if isinstance(value, dict):
        return {k: _deep_copy(v) for k, v in value.items()}
    if isinstance(value, list):
        return [_deep_copy(v) for v in value]
    return value


def canonical_signing_input(envelope):
    """Return the bytes that HMAC is computed over.

    The envelope's ``auth.sig`` is replaced with the empty string, optional
    fields are stripped, and the result is canonical-marshalled (sorted
    keys, no whitespace).
    """
    clone = _deep_copy(envelope)
    if "auth" not in clone or not isinstance(clone["auth"], dict):
        clone["auth"] = {}
    clone["auth"]["sig"] = ""
    return canonical_marshal(_strip_empty_optionals(clone))


def sign(envelope, psk):
    """Compute HMAC-SHA256 over *envelope* and stamp the signature in place.

    The lowercase-hex digest is stored in ``envelope['auth']['sig']``.
    Mutates and returns *envelope*.
    """
    if not psk:
        raise ProtocolError("sign: empty psk")
    canon = canonical_signing_input(envelope)
    digest = hmac.new(psk, canon, hashlib.sha256).hexdigest()
    envelope.setdefault("auth", {})
    envelope["auth"]["sig"] = digest
    return envelope


def verify_sig(envelope, psk):
    """Recompute HMAC-SHA256 and constant-time compare against the envelope sig.

    :returns: ``None`` on success.
    :raises ProtocolError: on any verification failure.
    """
    if not psk:
        raise ProtocolError("verify: empty psk")
    auth = envelope.get("auth") or {}
    if auth.get("alg") != AUTH_ALG:
        raise ProtocolError("verify: unsupported alg: {0!r}".format(auth.get("alg")))
    if auth.get("kid") != AUTH_KID:
        raise ProtocolError("verify: unsupported kid: {0!r}".format(auth.get("kid")))
    sig_hex = auth.get("sig")
    if not sig_hex:
        raise ProtocolError("verify: empty sig")
    try:
        got = binascii.unhexlify(sig_hex)
    except (TypeError, binascii.Error) as exc:
        raise ProtocolError("verify: invalid hex sig") from exc
    canon = canonical_signing_input(envelope)
    expected = hmac.new(psk, canon, hashlib.sha256).digest()
    if not hmac.compare_digest(expected, got):
        raise ProtocolError("verify: signature mismatch")


def write_frame(stream, envelope):
    """Encode and length-prefix-write the envelope to *stream*.

    Stream must be a writable bytes stream (e.g. ssl.SSLSocket.makefile('wb')
    or anything with a write(bytes) method).
    """
    validate(envelope)
    body = canonical_marshal(_strip_empty_optionals(envelope))
    write_raw(stream, body)


def write_raw(stream, body):
    """Write a length-prefixed raw body. Caller has already encoded JSON."""
    if len(body) > MAX_ENVELOPE_BYTES:
        raise TooLarge("envelope exceeds MAX_ENVELOPE_BYTES")
    hdr = struct.pack("!I", len(body))
    stream.write(hdr)
    stream.write(body)


def read_frame(stream):
    """Read a length-prefixed envelope and parse the JSON.

    Returns a dict on success, None on clean EOF before any header bytes are
    received.
    """
    body = read_raw(stream)
    if body is None:
        return None
    return json.loads(body.decode("utf-8"))


def read_raw(stream):
    """Read the framing header and the body bytes.

    :returns: the body bytes on success, or ``None`` on clean EOF before any
        bytes are read.
    :raises TooLarge: when the declared length exceeds
        :data:`MAX_ENVELOPE_BYTES`.
    """
    hdr = _read_exact(stream, 4)
    if hdr is None:
        return None
    length = struct.unpack("!I", hdr)[0]
    if length > MAX_ENVELOPE_BYTES:
        raise TooLarge("envelope exceeds MAX_ENVELOPE_BYTES")
    body = _read_exact(stream, length)
    if body is None:
        raise ProtocolError("unexpected EOF reading body")
    return body


def _read_exact(stream, n):
    """Read exactly n bytes; return None on clean EOF before any data."""
    chunks = []
    remaining = n
    while remaining > 0:
        chunk = stream.read(remaining)
        if not chunk:
            if not chunks:
                return None
            raise ProtocolError("unexpected EOF after {0} bytes".format(n - remaining))
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def new_id(prefix):
    """Generate a fresh envelope id under prefix (e.g. PREFIX_MESSAGE).

    Format: '<prefix>_<26-char-base32>' matching Go internal/arcp.NewID.
    """
    if not prefix:
        raise ProtocolError("empty id prefix")
    raw = os.urandom(16)
    return prefix + "_" + _crockford_encode(raw)


def _crockford_encode(data):
    """Encode bytes as 26-char base32 in our Crockford-like alphabet.

    Matches Go's
    ``base32.NewEncoding(_CROCKFORD_ALPHABET).WithPadding(NoPadding)``.
    """
    return base64.b32encode(data).decode("ascii").rstrip("=").translate(_CROCKFORD_TRANS)


def format_timestamp(dt=None):
    """Return an RFC 3339 / ISO 8601 timestamp string in UTC.

    Microsecond precision with trailing ``Z``. Matches
    ``Go internal/arcp.FormatTimestamp``.
    """
    if dt is None:
        dt = datetime.datetime.utcnow()
    # Python's strftime %f gives 6-digit microseconds.
    return dt.strftime("%Y-%m-%dT%H:%M:%S.%fZ")
