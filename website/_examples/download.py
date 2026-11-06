import os
import requests

base = os.environ["TRIBUTARY_URL"]
headers = {"Authorization": f"Bearer {os.environ['TRIBUTARY_KEY']}"}

file_id = os.environ["FILE_ID"]

with requests.get(f"{base}/api/v1/files/{file_id}/content", headers=headers, stream=True) as response:
    response.raise_for_status()
    with open("report.pdf", "wb") as out:
        for piece in response.iter_content(chunk_size=1024 * 1024):
            out.write(piece)
