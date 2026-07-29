curl -sS -X POST "$ARGENTUM_BASE_URL/v1/reports/render" \
  -H "Authorization: Bearer $ARGENTUM_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Accept: application/pdf" \
  -H "Idempotency-Key: $(uuidgen)" \
  --data-binary @spec.json \
  -D headers.txt \
  -o revenue.pdf
