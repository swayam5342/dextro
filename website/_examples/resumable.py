import os
import requests

base = os.environ["TRIBUTARY_URL"]
headers = {"Authorization": f"Bearer {os.environ['TRIBUTARY_KEY']}"}

PIECE = 8 * 1024 * 1024  # every piece but the last must be a multiple of 256 KiB
path = "big.iso"
upload_id = os.environ.get("UPLOAD_ID")  # set this to pick up an upload that stopped

if not upload_id:
    started = requests.post(
        f"{base}/api/v1/uploads",
        headers=headers,
        json={"name": path, "size": os.path.getsize(path)},
    )
    started.raise_for_status()
    upload_id = started.json()["id"]
print("Upload", upload_id)

# Ask where things really stand, because the account may have kept less than was sent.
session = requests.get(f"{base}/api/v1/uploads/{upload_id}", headers=headers)
session.raise_for_status()
session = session.json()

with open(path, "rb") as file:
    while "file" not in session:
        file.seek(session["received"])
        response = requests.put(
            f"{base}/api/v1/uploads/{upload_id}/chunk",
            params={"offset": session["received"]},
            headers=headers,
            data=file.read(PIECE),
        )
        response.raise_for_status()
        session = response.json()

print(session["file"]["id"])
