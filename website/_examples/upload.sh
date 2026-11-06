# The body is the file itself, not a form.
curl -X POST "$TRIBUTARY_URL/api/v1/files?name=report.pdf" \
  -H "Authorization: Bearer $TRIBUTARY_KEY" \
  -H "Content-Type: application/pdf" \
  --data-binary @report.pdf
