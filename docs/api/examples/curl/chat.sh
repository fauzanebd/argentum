curl -sSN -X POST "$ARGENTUM_BASE_URL/v1/chat" \
  -H "Authorization: Bearer $ARGENTUM_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"message":"What was total revenue in December 2024?","user_ref":"quickstart"}'
