import os
import requests

base = os.environ["TRIBUTARY_URL"]
headers = {"Authorization": f"Bearer {os.environ['TRIBUTARY_KEY']}"}

response = requests.get(f"{base}/api/v1/files", headers=headers)
response.raise_for_status()
listing = response.json()

for folder in listing["folders"]:
    print("folder", folder["name"], folder["id"])
for file in listing["files"]:
    print("file", file["name"], file["size"], file["id"])
