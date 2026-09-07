#!/usr/bin/env python3
"""Check committed Maven versions and Swift revisions against OSV advisories."""
import json
from pathlib import Path
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def dependencies():
    labels, queries, seen = [], [], set()
    lockfiles = list((ROOT / "android").glob("*/gradle.lockfile"))
    if len(lockfiles) != 2:
        raise SystemExit("Expected both Android module lockfiles")
    for lockfile in lockfiles:
        for line in lockfile.read_text().splitlines():
            if line.startswith("#") or "=" not in line:
                continue
            parts = line.split("=", 1)[0].split(":")
            if len(parts) != 3 or tuple(parts) in seen:
                continue
            seen.add(tuple(parts))
            group, name, version = parts
            labels.append(f"{group}:{name}:{version}")
            queries.append({
                "package": {"ecosystem": "Maven", "name": f"{group}:{name}"},
                "version": version,
            })
    for pin in json.loads((ROOT / "Package.resolved").read_text())["pins"]:
        labels.append(f"{pin['identity']}:{pin['state'].get('version', '')}")
        queries.append({"commit": pin["state"]["revision"]})
    return labels, queries


def main():
    labels, queries = dependencies()
    request = urllib.request.Request(
        "https://api.osv.dev/v1/querybatch",
        data=json.dumps({"queries": queries}).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=60) as response:
        results = json.load(response)["results"]
    if len(results) != len(queries):
        raise SystemExit("OSV returned an incomplete response")
    findings = [
        {"package": label, "vulns": result["vulns"]}
        for label, result in zip(labels, results) if result.get("vulns")
    ]
    print(json.dumps({"dependenciesChecked": len(queries), "findings": findings}, indent=2))
    return 1 if findings else 0


if __name__ == "__main__":
    raise SystemExit(main())
