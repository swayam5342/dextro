import os
import requests

base = os.environ["TRIBUTARY_URL"]
headers = {"Authorization": f"Bearer {os.environ['TRIBUTARY_KEY']}"}

# Passing an open file streams it, so a large file doesn't fill your memory.
with open("report.pdf", "rb") as body:
    response = requests.post(
        f"{base}/api/v1/files",
        params={"name": "report.pdf"},
        headers={**headers, "Content-Type": "application/pdf"},
        data=body,
    )
response.raise_for_status()
print(response.json()["id"])
