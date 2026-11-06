# Make a folder. Use a folder's ID as parent_id to nest it.
curl -X POST "$TRIBUTARY_URL/api/v1/folders" \
  -H "Authorization: Bearer $TRIBUTARY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "Reports", "parent_id": null}'

# Move a file into it, then rename the file.
curl -X POST "$TRIBUTARY_URL/api/v1/files/$FILE_ID/move" \
  -H "Authorization: Bearer $TRIBUTARY_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"folder_id\": \"$FOLDER_ID\"}"

curl -X PATCH "$TRIBUTARY_URL/api/v1/files/$FILE_ID" \
  -H "Authorization: Bearer $TRIBUTARY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "March report.pdf"}'
