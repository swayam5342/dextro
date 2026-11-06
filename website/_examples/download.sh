curl "$TRIBUTARY_URL/api/v1/files/$FILE_ID/content" \
  -H "Authorization: Bearer $TRIBUTARY_KEY" \
  -o report.pdf
