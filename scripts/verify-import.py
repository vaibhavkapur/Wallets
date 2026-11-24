#!/usr/bin/env python3
"""Compare the recorded import with each source commit in this repository's history."""

import json
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def git(*args):
    return subprocess.check_output(["git", "-C", ROOT, *args])


def main():
    manifest = json.loads((ROOT / "migration/sources.json").read_text())
    total = 0
    for project in manifest["repositories"]:
        imported = project.get("import_commit", manifest["import_commit"])
        git("merge-base", "--is-ancestor", imported, "HEAD")
        source = project["commit"]
        path = project["path"]
        git("merge-base", "--is-ancestor", source, imported)
        source_tree = git("rev-parse", source + "^{tree}").decode().strip()
        nested_tree = git("rev-parse", f"{imported}:{path}").decode().strip()
        if source_tree != nested_tree or source_tree != project["tree"]:
            raise SystemExit(f"Tree mismatch for {path}")
        entries = git("ls-tree", "-rz", nested_tree).count(b"\0")
        if entries != project["file_count"]:
            raise SystemExit(f"Tracked entry count differs for {path}")
        total += entries
    print(f"Verified {len(manifest['repositories'])} project trees and {total} tracked entries at their recorded imports.")


if __name__ == "__main__":
    main()
