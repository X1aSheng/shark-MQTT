#!/usr/bin/env python3
"""Run a command on a remote host over SSH with password auth.

Exists because this box has no sshpass and the second cloud host accepts only
password auth. Wrapping paramiko keeps the command line identical to ssh:

    python remote.py <host> <user> <password> "<command>"

For anything long-running, prefer running it detached on the remote side and
polling a log, rather than holding the channel open:

    python remote.py host root pw "nohup <cmd> > /tmp/x.log 2>&1 & echo started"
"""

import sys
import paramiko


def main() -> int:
    if len(sys.argv) < 5:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    host, user, password, command = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    timeout = float(sys.argv[5]) if len(sys.argv) > 5 else 120.0

    client = paramiko.SSHClient()
    # The hosts here are throwaway verification targets whose host key changes
    # between rebuilds; pinning it would be theatre. Accept on first use.
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    try:
        client.connect(
            host,
            username=user,
            password=password,
            timeout=20,
            banner_timeout=30,
            auth_timeout=30,
        )
    except Exception as exc:  # noqa: BLE001 - report any failure the same way
        print(f"connect failed: {exc}", file=sys.stderr)
        return 1

    try:
        channel = client.get_transport().open_session()
        channel.settimeout(timeout)
        channel.exec_command(command)

        out = bytearray()
        err = bytearray()
        while True:
            if channel.recv_ready():
                chunk = channel.recv(65536)
                if not chunk:
                    break
                out.extend(chunk)
            if channel.recv_stderr_ready():
                err.extend(channel.recv_stderr(65536))
            if channel.exit_status_ready() and not channel.recv_ready() and not channel.recv_stderr_ready():
                break
        code = channel.recv_exit_status()
    except Exception as exc:  # noqa: BLE001
        print(f"exec failed: {exc}", file=sys.stderr)
        client.close()
        return 1

    sys.stdout.write(out.decode("utf-8", errors="replace"))
    sys.stderr.write(err.decode("utf-8", errors="replace"))
    client.close()
    return code


if __name__ == "__main__":
    sys.exit(main())