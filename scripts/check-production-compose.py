#!/usr/bin/env python3
"""Validate the rendered production Compose security and deployment contract."""

import json
import re
import sys


def require(condition: bool, message: str, errors: list[str]) -> None:
    if not condition:
        errors.append(message)


def main() -> int:
    document = json.load(sys.stdin)
    errors: list[str] = []
    services = document.get("services", {})
    require(set(services) == {"memento"}, "production must define exactly one memento service", errors)
    service = services.get("memento", {})
    require(
        re.fullmatch(r"ghcr\.io/robinjoseph08/memento@sha256:[0-9a-f]{64}", service.get("image", ""))
        is not None,
        "image must use the fixed GHCR repository and a sha256 digest",
        errors,
    )
    require("build" not in service, "production must not build an image", errors)
    require(service.get("read_only") is True, "root filesystem must be read-only", errors)
    require(service.get("init") is True, "container init must be enabled", errors)
    require(service.get("cap_drop") == ["ALL"], "all Linux capabilities must be dropped", errors)
    require(
        "no-new-privileges:true" in service.get("security_opt", []),
        "no-new-privileges must be enabled",
        errors,
    )
    require(service.get("restart") == "unless-stopped", "restart policy must be unless-stopped", errors)
    require(service.get("stop_signal") == "SIGTERM", "stop signal must be SIGTERM", errors)
    require(service.get("stop_grace_period") == "15s", "stop grace period must be 15 seconds", errors)
    require("/tmp:mode=1777" in service.get("tmpfs", []), "temporary storage must use tmpfs", errors)

    mounts = {mount.get("target"): mount for mount in service.get("volumes", [])}
    for target in ("/etc/memento/memento.yaml", "/run/secrets"):
        require(target in mounts, f"required mount is missing: {target}", errors)
        require(mounts.get(target, {}).get("read_only") is True, f"mount must be read-only: {target}", errors)

    environment = service.get("environment", {})
    for name in ("MEMENTO_DATABASE_URL_FILE", "MEMENTO_IMMICH_API_KEY_FILE", "MEMENTO_SECURITY_SECRET_FILE"):
        require(str(environment.get(name, "")).startswith("/run/secrets/"), f"{name} must use a secret file", errors)

    networks = document.get("networks", {})
    require(
        networks.get("memento-private", {}).get("external") is True,
        "the dependency network must be external",
        errors,
    )

    if errors:
        for error in errors:
            print(f"production Compose contract: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
