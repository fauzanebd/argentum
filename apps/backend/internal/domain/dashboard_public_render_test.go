package domain

import "encoding/json"

// renderForTest serialises a dashboard the way the share route does, so a test
// can assert on what actually goes over the wire rather than on field names.
func renderForTest(d *Dashboard) string {
	b, err := json.Marshal(d)
	if err != nil {
		return "marshal failed: " + err.Error()
	}
	return string(b)
}
