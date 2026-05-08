# -*- coding: utf-8 -*-
"""Phase 3 echo server (Python 3.4-compatible).

Reads ARCP envelopes over TLS, verifies HMAC, and echoes them back with the
id and type suffixed by '.echo'. This is a one-shot verification stub used
to prove the wire protocol works on the real XP VM. Phase 4 replaces it
with a real agent that actually dispatches tools.

Usage:
    python C:\\xpc\\echo_server.py --port 9579 \\
        --cert C:\\xpc\\server.crt --key C:\\xpc\\server.key \\
        --psk-file C:\\xpc\\psk.hex

PSK file: lowercase-hex-encoded 32 bytes (matches what xpc bootstrap will
produce in Phase 4). Exactly 64 hex characters, optional trailing newline.
"""
from __future__ import absolute_import, print_function

import argparse
import binascii
import os
import socket
import ssl
import sys
import threading

# Ensure same-dir import for arcp (the script dir is on sys.path when run
# directly, but be defensive in case it's loaded another way).
HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

import arcp  # noqa: E402


def serve_one_client(tls_conn, psk):
    """Read frames, echo each one back. Closes the conn on EOF or error."""
    rfile = tls_conn.makefile("rb")
    wfile = tls_conn.makefile("wb")
    try:
        while True:
            envelope = arcp.read_frame(rfile)
            if envelope is None:
                return
            try:
                arcp.verify_sig(envelope, psk)
            except arcp.ProtocolError as exc:
                print("auth failed: {0}".format(exc), file=sys.stderr)
                sys.stderr.flush()
                return

            envelope["id"] = envelope["id"] + ".echo"
            envelope["type"] = envelope["type"] + ".echo"
            arcp.sign(envelope, psk)
            arcp.write_frame(wfile, envelope)
            wfile.flush()
    except Exception as exc:
        print("client error: {0}".format(exc), file=sys.stderr)
        sys.stderr.flush()
    finally:
        try:
            rfile.close()
        except Exception:
            pass
        try:
            wfile.close()
        except Exception:
            pass
        try:
            tls_conn.close()
        except Exception:
            pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=9579)
    parser.add_argument("--bind", default="0.0.0.0")
    parser.add_argument("--cert", required=True)
    parser.add_argument("--key", required=True)
    parser.add_argument("--psk-file", required=True)
    args = parser.parse_args()

    with open(args.psk_file, "r") as f:
        psk = binascii.unhexlify(f.read().strip())
    if len(psk) != 32:
        raise SystemExit("psk file must be exactly 32 hex bytes (got {0})".format(len(psk)))

    ctx = ssl.SSLContext(ssl.PROTOCOL_TLSv1_2)
    ctx.load_cert_chain(args.cert, args.key)
    ctx.set_ciphers("ECDHE-RSA-AES256-GCM-SHA384:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-SHA256")

    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind((args.bind, args.port))
    sock.listen(5)
    print("echo server listening on {0}:{1}".format(args.bind, args.port))
    sys.stdout.flush()

    while True:
        client_sock, addr = sock.accept()
        print("connection from {0}".format(addr))
        sys.stdout.flush()
        try:
            tls_conn = ctx.wrap_socket(client_sock, server_side=True)
        except (ssl.SSLError, OSError) as exc:
            print("TLS handshake failed: {0}".format(exc), file=sys.stderr)
            sys.stderr.flush()
            try:
                client_sock.close()
            except Exception:
                pass
            continue
        t = threading.Thread(target=serve_one_client, args=(tls_conn, psk))
        t.daemon = True
        t.start()


if __name__ == "__main__":
    main()
