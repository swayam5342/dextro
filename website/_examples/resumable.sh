# Set UPLOAD_ID to pick up an upload that stopped. Needs jq.
FILE=big.iso
SIZE=$(wc -c < "$FILE")
PIECE=$((8 * 1024 * 1024)) # every piece but the last must be a multiple of 256 KiB
AUTH="Authorization: Bearer $TRIBUTARY_KEY"

if [ -z "$UPLOAD_ID" ]; then
  UPLOAD_ID=$(curl -s -X POST "$TRIBUTARY_URL/api/v1/uploads" -H "$AUTH" \
    -H "Content-Type: application/json" \
    -d "{\"name\": \"$FILE\", \"size\": $SIZE}" | jq -r .id)
fi
echo "Upload $UPLOAD_ID"

SESSION=$(curl -s "$TRIBUTARY_URL/api/v1/uploads/$UPLOAD_ID" -H "$AUTH")
while [ "$(echo "$SESSION" | jq -r '.file.id // empty')" = "" ]; do
  OFFSET=$(echo "$SESSION" | jq -r .received)
  SESSION=$(tail -c +$((OFFSET + 1)) "$FILE" | head -c "$PIECE" |
    curl -s -X PUT "$TRIBUTARY_URL/api/v1/uploads/$UPLOAD_ID/chunk?offset=$OFFSET" \
      -H "$AUTH" --data-binary @-)
done
echo "$SESSION" | jq -r .file.id
