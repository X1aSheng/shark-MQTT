#!/usr/bin/env python3
"""Build a minimal OCI/Docker image archive for shark-mqtt without Docker.

Why this exists
---------------
Verification on a constrained cloud host found two obstacles:

  * `apt-get install docker.io` reported "Unable to locate package", and the
    Aliyun docker-ce route was too slow to be useful next to a k3s control plane.
  * Even where Docker is present, building the image there competes with the
    cluster for memory.

A release binary plus a tiny rootfs is all the image needs, so this script
assembles the archive directly and hands it to `ctr images import` (k3s) or
`docker load`.

The result is deliberately minimal: no shell, no package manager, no coreutils.
That is a feature for a broker image, and it is also why the Helm chart uses the
native `sleep` preStop handler rather than an `exec` one -- see the chart.

Usage:
    python3 build-image.py --binary ./shark-mqtt --tag shark-mqtt:cloud \\
        --out ./shark-image.tar
"""

import argparse
import hashlib
import json
import os
import platform
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile


def sha256_file(path: str) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def check_binary_format(binary: str) -> None:
    """Refuse to package a binary that will not run in the image.

    A cross-compiled image is easy to get wrong by accident: `go build` on
    Windows happily produces a PE binary, packaging succeeds, the image
    imports, and the container only fails at run time with

        exec /usr/local/bin/shark-mqtt: exec format error

    That surfaces as a CrashLoopBackOff with an error that says nothing about
    how the image was built. Checking the magic bytes here turns a puzzling
    runtime failure into an immediate, explanatory build failure.
    """
    with open(binary, "rb") as fh:
        head = fh.read(20)

    if head[:4] == b"\x7fELF":
        machine = int.from_bytes(head[18:20], "little")  # e_machine
        # 0x3E = x86-64, 0xB7 = AArch64.
        arch = {0x3E: "x86_64", 0xB7: "aarch64"}.get(machine)
        if arch and arch != platform.machine():
            sys.exit(
                f"binary is ELF for {arch} but this host is {platform.machine()}; "
                "the image will not run here. Cross-compile instead://n"
                "  GOOS=linux GOARCH=amd64 go build -o shark-mqtt ./cmd/"
            )
        return

    if head[:2] == b"MZ":
        sys.exit(
            "binary is a Windows PE executable (MZ header), not an ELF binary.\n"
            "A Linux container image cannot run it -- the container would start "
            "and die with 'exec format error'.\n"
            "Build for Linux:\n"
            "  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' "
            "-o shark-mqtt ./cmd/"
        )

    # Unknown format: let the container runtime be the judge.
    print(f"warning: unrecognised binary format (magic {head[:4]!r}); "
          "the container runtime may reject it", file=sys.stderr)


def build(binary: str, tag: str, out: str) -> None:
    if not os.path.isfile(binary):
        sys.exit(f"binary not found: {binary}")
    check_binary_format(binary)

    workdir = tempfile.mkdtemp(prefix="shark-image-")
    try:
        # Minimal rootfs: the binary plus CA certificates for TLS.
        bin_dir = os.path.join(workdir, "rootfs", "usr", "local", "bin")
        os.makedirs(bin_dir)
        dest = os.path.join(bin_dir, "shark-mqtt")
        shutil.copy(binary, dest)
        os.chmod(dest, os.stat(dest).st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        ca_src = "/etc/ssl/certs/ca-certificates.crt"
        if os.path.exists(ca_src):
            ca_dir = os.path.join(workdir, "rootfs", "etc", "ssl", "certs")
            os.makedirs(ca_dir)
            shutil.copy(ca_src, ca_dir)

        # Two digests are needed and they are NOT interchangeable:
        #   diff_id = sha256 of the UNCOMPRESSED tar (what config.json records)
        #   blob    = sha256 of the GZIPPED tar   (what repositories maps to)
        # Build the layer with Python's tarfile and SET THE MODE EXPLICITLY.
        #
        # Doing this with the `tar` binary was the first attempt and it is
        # wrong on Windows in two separate ways: the bundled tar records the
        # NTFS-derived mode (0o666 for everything), and it does not support
        # --mode to correct it. The result imports perfectly and then fails at
        # runtime with
        #   exec: "/usr/local/bin/shark-mqtt": permission denied
        # which is silent until a pod actually starts.
        layer_tar = os.path.join(workdir, "layer.tar")
        with tarfile.open(layer_tar, "w", format=tarfile.GNU_FORMAT) as tar:
            for dirpath, dirnames, filenames in os.walk(os.path.join(workdir, "rootfs")):
                dirnames.sort()
                for name in sorted(dirnames) + sorted(filenames):
                    full = os.path.join(dirpath, name)
                    rel = os.path.relpath(full, os.path.join(workdir, "rootfs"))
                    info = tar.gettarinfo(full, arcname="./" + rel.replace(os.sep, "/"))
                    # Directories need the traverse bit; files get 0755 so the
                    # entrypoint is executable regardless of how it was copied.
                    info.mode = 0o755
                    info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    if info.isreg():
                        with open(full, "rb") as fh:
                            tar.addfile(info, fh)
                    else:
                        tar.addfile(info)

        diff_id = "sha256:" + sha256_file(layer_tar)

        layer_gz = os.path.join(workdir, "layer.tar.gz")
        subprocess.run(["gzip", "-9", "-c", layer_tar], stdout=open(layer_gz, "wb"), check=True)
        blob = "sha256:" + sha256_file(layer_gz)

        repo, _, tagname = tag.partition(":")
        tagname = tagname or "latest"

        # manifest.json: Layers MUST be plain file names. Writing
        # "sha256:<digest>/layer.tar" here makes ctr fail with
        # "failed to resolve layers" -- the digest-qualified form is what the
        # OCI layout uses, not the Docker archive format.
        with open(os.path.join(workdir, "manifest.json"), "w") as fh:
            json.dump([{"Config": "config.json", "RepoTags": [tag], "Layers": ["layer.tar.gz"]}], fh)

        with open(os.path.join(workdir, "config.json"), "w") as fh:
            json.dump(
                {
                    "architecture": "amd64",
                    "os": "linux",
                    "config": {
                        "Env": ["PATH=/usr/local/bin"],
                        "Entrypoint": ["/usr/local/bin/shark-mqtt"],
                        "WorkingDir": "/",
                        # Informational only; the broker reads its ports from the
                        # mounted config file. Kept in sync with values.yaml so a
                        # `kubectl describe pod` shows the expected surface.
                        "ExposedPorts": {
                            f"{port}/tcp": {} for port in (1883, 2883, 8083, 19090)
                        },
                        "Labels": {
                            "org.opencontainers.image.title": "shark-mqtt",
                            "org.opencontainers.image.description":
                                "High-performance MQTT 3.1.1/5.0 broker",
                        },
                    },
                    "rootfs": {"type": "layers", "diff_ids": [diff_id]},
                    "history": [{"created": "2026-01-01T00:00:00Z", "created_by": "shark-mqtt"}],
                },
                fh,
            )

        # repositories is what ctr reads to resolve RepoTags. Without it the
        # import succeeds but the tag never appears in `ctr images ls`.
        with open(os.path.join(workdir, "repositories"), "w") as fh:
            json.dump({repo: {tagname: blob}}, fh)

        with tarfile.open(out, "w") as tar:
            for name in ("manifest.json", "config.json", "layer.tar.gz", "repositories"):
                tar.add(os.path.join(workdir, name), arcname=name)

        print(f"built {out} ({os.path.getsize(out)} bytes)")
        print(f"  tag    : {tag}")
        print(f"  diff_id: {diff_id[:23]}...")
        print("\nimport with:")
        print(f"  k3s ctr images import {out}      # Kubernetes (k3s)")
        print(f"  docker load -i {out}             # plain Docker")
    finally:
        shutil.rmtree(workdir, ignore_errors=True)


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--binary", required=True, help="path to the compiled shark-mqtt binary")
    ap.add_argument("--tag", default="shark-mqtt:latest", help="image tag, e.g. shark-mqtt:1.2.3")
    ap.add_argument("--out", default="shark-image.tar", help="output archive path")
    args = ap.parse_args()
    build(args.binary, args.tag, args.out)


if __name__ == "__main__":
    main()