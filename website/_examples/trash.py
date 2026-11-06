import os
import requests

base = os.environ["TRIBUTARY_URL"]
headers = {"Authorization": f"Bearer {os.environ['TRIBUTARY_KEY']}"}

file_id = os.environ["FILE_ID"]

requests.post(f"{base}/api/v1/files/{file_id}/trash", headers=headers).raise_for_status()

trash = requests.get(f"{base}/api/v1/trash", headers=headers).json()
print(len(trash["files"]), "files in the trash")

requests.post(f"{base}/api/v1/files/{file_id}/restore", headers=headers).raise_for_status()
