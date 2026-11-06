# The top level. Add ?folder_id=<id> for a folder.
curl "$TRIBUTARY_URL/api/v1/files" \
  -H "Authorization: Bearer $TRIBUTARY_KEY"
