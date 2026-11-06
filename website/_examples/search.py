import os
import requests

base = os.environ["TRIBUTARY_URL"]
headers = {"Authorization": f"Bearer {os.environ['TRIBUTARY_KEY']}"}

response = requests.get(f"{base}/api/v1/search", headers=headers, params={"q": "report"})
response.raise_for_status()
results = response.json()

for file in results["files"]:
    print(file["name"], file["id"])
