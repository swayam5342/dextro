curl -G "$TRIBUTARY_URL/api/v1/search" \
  -H "Authorization: Bearer $TRIBUTARY_KEY" \
  --data-urlencode "q=report"
