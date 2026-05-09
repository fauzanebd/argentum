package document

import (
	"bytes"
	"encoding/csv"
	"fmt"
)

func RenderCSV(spec *Spec) ([]byte, error) {
	t := spec.Content.Table
	if t == nil {
		return nil, fmt.Errorf("csv: content.table missing")
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(t.Columns); err != nil {
		return nil, fmt.Errorf("csv: write header: %w", err)
	}
	for i, row := range t.Rows {
		if err := w.Write(row); err != nil {
			return nil, fmt.Errorf("csv: write row %d: %w", i, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("csv: flush: %w", err)
	}
	return buf.Bytes(), nil
}
