curl -sS -X POST "$ARGENTUM_BASE_URL/v1/reports" \
  -H "Authorization: Bearer $ARGENTUM_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"prompt":"Total revenue by month for 2024, with a bar chart.","format":"pdf","user_ref":"quickstart"}'
