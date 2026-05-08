# -*- coding: utf-8 -*-
"""Tests for agent.arcp.

These tests run on the host's modern Python (3.x) but exercise the
3.4-compatible agent module. They cover encode/decode round-trips, HMAC
sign/verify, framing, and ID generation. Corpus parity with Go is in a
separate test file (test_corpus.py).
"""
from __future__ import absolute_import

import io
import os
import sys

import pytest

# Add the parent agent/ directory to sys.path so we can import the module
# without needing a package layout.
HERE = os.path.dirname(os.path.abspath(__file__))
AGENT_DIR = os.path.dirname(HERE)
sys.path.insert(0, AGENT_DIR)

import arcp  # noqa: E402


PSK = b"\x00" * 32


def sample_envelope():
    return arcp.new_envelope(
        envelope_id="msg_01habcdef1234567890abcdef",
        msg_type=arcp.TYPE_PING,
        timestamp="2026-05-08T18:21:00.000000Z",
    )


def test_new_envelope_has_required_fields():
    e = sample_envelope()
    assert e["arcp"] == arcp.VERSION
    assert e["auth"]["alg"] == arcp.AUTH_ALG
    assert e["auth"]["kid"] == arcp.AUTH_KID
    arcp.validate(e)


@pytest.mark.parametrize("mut,expect_substr", [
    (lambda e: e.update(arcp=""), "unsupported arcp version"),
    (lambda e: e.update(arcp="9.9"), "unsupported arcp version"),
    (lambda e: e.update(id=""), "empty envelope id"),
    (lambda e: e.update(type=""), "empty envelope type"),
    (lambda e: e.update(timestamp=""), "empty timestamp"),
    (lambda e: e["auth"].update(alg="MD5"), "unsupported auth alg"),
    (lambda e: e["auth"].update(kid="v99"), "unsupported auth kid"),
    (lambda e: e.update(payload=None), "nil payload"),
])
def test_validate_rejects_missing_fields(mut, expect_substr):
    e = sample_envelope()
    mut(e)
    with pytest.raises(arcp.ProtocolError) as exc:
        arcp.validate(e)
    assert expect_substr in str(exc.value)


def test_canonical_marshal_sorts_keys_at_every_level():
    v = {
        "z": 1,
        "a": {"y": 2, "b": 3},
        "m": [{"q": 9, "p": 8}],
    }
    got = arcp.canonical_marshal(v)
    assert got == b'{"a":{"b":3,"y":2},"m":[{"p":8,"q":9}],"z":1}'


def test_canonical_marshal_no_html_escape():
    v = {"k": "<hello & 'world'>"}
    got = arcp.canonical_marshal(v)
    # Python doesn't HTML-escape by default; the literal characters should
    # survive untouched.
    assert b"<" in got
    assert b"&" in got
    # The 6-char escape sequence should NOT appear.
    assert b"\\u003c" not in got


def test_canonical_signing_input_blanks_sig():
    e = sample_envelope()
    e["auth"]["sig"] = "deadbeef"
    canon = arcp.canonical_signing_input(e)
    assert b'"sig":""' in canon
    assert b"deadbeef" not in canon
    # Ensure the original envelope wasn't mutated.
    assert e["auth"]["sig"] == "deadbeef"


def test_sign_then_verify_round_trip():
    e = sample_envelope()
    arcp.sign(e, PSK)
    assert e["auth"]["sig"]  # non-empty
    arcp.verify_sig(e, PSK)  # no raise


def test_verify_rejects_tampered_envelope():
    e = sample_envelope()
    arcp.sign(e, PSK)
    e["id"] = "msg_TAMPERED"
    with pytest.raises(arcp.ProtocolError) as exc:
        arcp.verify_sig(e, PSK)
    assert "signature mismatch" in str(exc.value)


def test_verify_rejects_wrong_psk():
    e = sample_envelope()
    arcp.sign(e, PSK)
    with pytest.raises(arcp.ProtocolError):
        arcp.verify_sig(e, b"\x01" * 32)


def test_verify_rejects_bad_hex_sig():
    e = sample_envelope()
    e["auth"]["sig"] = "not-hex"
    with pytest.raises(arcp.ProtocolError) as exc:
        arcp.verify_sig(e, PSK)
    assert "invalid hex sig" in str(exc.value)


def test_verify_rejects_empty_sig():
    e = sample_envelope()
    with pytest.raises(arcp.ProtocolError) as exc:
        arcp.verify_sig(e, PSK)
    assert "empty sig" in str(exc.value)


def test_sign_deterministic():
    e1 = sample_envelope()
    e2 = sample_envelope()
    arcp.sign(e1, PSK)
    arcp.sign(e2, PSK)
    assert e1["auth"]["sig"] == e2["auth"]["sig"]


def test_write_frame_then_read_frame():
    e = sample_envelope()
    arcp.sign(e, PSK)
    buf = io.BytesIO()
    arcp.write_frame(buf, e)
    buf.seek(0)
    got = arcp.read_frame(buf)
    assert got["id"] == e["id"]
    assert got["type"] == e["type"]
    assert got["auth"]["sig"] == e["auth"]["sig"]


def test_write_frame_includes_length_prefix():
    e = sample_envelope()
    arcp.sign(e, PSK)
    buf = io.BytesIO()
    arcp.write_frame(buf, e)
    wire = buf.getvalue()
    assert len(wire) >= 4
    import struct as _s
    declared = _s.unpack("!I", wire[:4])[0]
    assert declared == len(wire) - 4


def test_read_raw_returns_none_on_empty():
    assert arcp.read_raw(io.BytesIO(b"")) is None


def test_read_raw_rejects_overlength():
    # 4-byte big-endian uint32 max.
    overlength_hdr = b"\xff\xff\xff\xff"
    with pytest.raises(arcp.TooLarge):
        arcp.read_raw(io.BytesIO(overlength_hdr + b"x" * 100))


def test_partial_read_handled():
    e = sample_envelope()
    arcp.sign(e, PSK)
    buf = io.BytesIO()
    arcp.write_frame(buf, e)
    wire = buf.getvalue()

    class OneByteReader:
        def __init__(self, src):
            self.src = src
            self.pos = 0

        def read(self, n):
            if self.pos >= len(self.src):
                return b""
            chunk = self.src[self.pos:self.pos + 1]
            self.pos += 1
            return chunk

    got = arcp.read_frame(OneByteReader(wire))
    assert got["id"] == e["id"]


def test_new_id_format():
    for prefix in (arcp.PREFIX_MESSAGE, arcp.PREFIX_SESSION, arcp.PREFIX_JOB,
                   arcp.PREFIX_STREAM, arcp.PREFIX_TRACE, arcp.PREFIX_SPAN):
        got = arcp.new_id(prefix)
        assert got.startswith(prefix + "_")
        body = got[len(prefix) + 1:]
        assert len(body) == 26
        # Crockford alphabet only.
        assert all(c in arcp._CROCKFORD_ALPHABET for c in body)


def test_new_id_unique():
    seen = set()
    for _ in range(1000):
        got = arcp.new_id(arcp.PREFIX_MESSAGE)
        assert got not in seen
        seen.add(got)


def test_new_id_rejects_empty_prefix():
    with pytest.raises(arcp.ProtocolError):
        arcp.new_id("")


def test_format_timestamp_microsecond_precision():
    import datetime
    dt = datetime.datetime(2026, 5, 8, 18, 21, 0, 123456)
    got = arcp.format_timestamp(dt)
    assert got == "2026-05-08T18:21:00.123456Z"
