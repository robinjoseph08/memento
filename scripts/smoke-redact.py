#!/usr/bin/env python3
"""Redact smoke command output before writing logs or CI artifacts."""

import os
import re
import sys

# Include inherited credentials even though the smoke itself uses disposable ones.
secrets = sorted(
    {value for key, value in os.environ.items()
     if re.search(r"password|secret|token|api.?key|database_url", key, re.I)
     and len(value) >= 8},
    key=len, reverse=True,
)
for line in sys.stdin:
    for secret in secrets:
        line = line.replace(secret, "[REDACTED]")
    line = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", line)
    line = re.sub(r"(\w+://)[^\s/@]+:[^\s/@]+@", r"\1[REDACTED]@", line)
    # Drop the rest of credential-bearing lines, including JSON and HTTP headers.
    line = re.sub(
        r"(?i)([\"']?(?:authorization|(?:set-)?cookie|(?:[\w-]*[_-])?"
        r"(?:password|secret|token)|(?:x-)?api[_-]?key|accessToken|refreshToken)"
        r"[\"']?\s*[:=]\s*).*", r"\1[REDACTED]", line,
    )
    # Unknown generated tokens can appear in an error without a field name.
    # Keep digests and UUIDs useful for diagnostics, but not opaque token strings.
    line = re.sub(r"(?<![\w:])[A-Za-z0-9_+/=-]{40,}(?![\w])", "[REDACTED]", line)
    sys.stdout.write(line)
    sys.stdout.flush()
