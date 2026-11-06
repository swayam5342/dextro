"""Copies a local folder into Tributary. Run it every night with cron.

    python backup.py ~/Documents "Laptop backup"

Files already there with the same name and size are skipped.
"""
import mimetypes
import os
import sys
from pathlib import Path

import requests

base = os.environ["TRIBUTARY_URL"]
headers = {"Authorization": f"Bearer {os.environ['TRIBUTARY_KEY']}"}


def call(method, path, **options):
    options["headers"] = {**headers, **options.get("headers", {})}
    response = requests.request(method, f"{base}/api/v1{path}", **options)
    response.raise_for_status()
    return response.json()


def find_or_make_folder(name):
    for folder in call("GET", "/files")["folders"]:
        if folder["name"] == name:
            return folder["id"]
    return call("POST", "/folders", json={"name": name, "parent_id": None})["id"]


def main():
    source, name = Path(sys.argv[1]), sys.argv[2]
    folder_id = find_or_make_folder(name)
    stored = call("GET", "/files", params={"folder_id": folder_id})["files"]
    sizes = {file["name"]: file["size"] for file in stored}

    sent = skipped = 0
    for path in sorted(source.iterdir()):
        if not path.is_file():
            continue
        if sizes.get(path.name) == path.stat().st_size:
            skipped += 1
            continue
        kind = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
        with path.open("rb") as body:
            call(
                "POST",
                "/files",
                params={"name": path.name, "folder_id": folder_id},
                headers={"Content-Type": kind},
                data=body,
            )
        sent += 1
    print(f"{sent} uploaded, {skipped} already there")


main()
