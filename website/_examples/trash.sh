# Into the trash, then back out again.
curl -X POST "$TRIBUTARY_URL/api/v1/files/$FILE_ID/trash" \
  -H "Authorization: Bearer $TRIBUTARY_KEY"

curl "$TRIBUTARY_URL/api/v1/trash" \
  -H "Authorization: Bearer $TRIBUTARY_KEY"

curl -X POST "$TRIBUTARY_URL/api/v1/files/$FILE_ID/restore" \
  -H "Authorization: Bearer $TRIBUTARY_KEY"
