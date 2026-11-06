import os
import requests

base = os.environ["TRIBUTARY_URL"]
headers = {"Authorization": f"Bearer {os.environ['TRIBUTARY_KEY']}"}

file_id = os.environ["FILE_ID"]

folder = requests.post(
    f"{base}/api/v1/folders",
    headers=headers,
    json={"name": "Reports", "parent_id": None},
)
folder.raise_for_status()
folder_id = folder.json()["id"]

requests.post(
    f"{base}/api/v1/files/{file_id}/move",
    headers=headers,
    json={"folder_id": folder_id},
).raise_for_status()

requests.patch(
    f"{base}/api/v1/files/{file_id}",
    headers=headers,
    json={"name": "March report.pdf"},
).raise_for_status()
