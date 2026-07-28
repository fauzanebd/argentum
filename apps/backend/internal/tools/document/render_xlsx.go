package document

import (
	"bytes"
	"fmt"

	"github.com/xuri/excelize/v2"
)

func RenderXLSX(spec *Spec) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	// excelize starts with a sheet called "Sheet1". Track whether we've
	// reused it yet so multi-sheet specs don't leave a stray empty sheet.
	const defaultSheet = "Sheet1"
	defaultUsed := false

	writeSheet := func(name string, columns []string, rows [][]string) error {
		sheet := name
		if sheet == "" {
			sheet = "Sheet1"
		}
		if !defaultUsed {
			if sheet != defaultSheet {
				if _, err := f.NewSheet(sheet); err != nil {
					return fmt.Errorf("xlsx: new sheet %q: %w", sheet, err)
				}
				if err := f.DeleteSheet(defaultSheet); err != nil {
					return fmt.Errorf("xlsx: delete default sheet: %w", err)
				}
			}
			defaultUsed = true
		} else if _, err := f.NewSheet(sheet); err != nil {
			return fmt.Errorf("xlsx: new sheet %q: %w", sheet, err)
		}

		// Header row.
		if len(columns) > 0 {
			row := make([]interface{}, len(columns))
			for i, c := range columns {
				row[i] = c
			}
			if err := f.SetSheetRow(sheet, "A1", &row); err != nil {
				return fmt.Errorf("xlsx: write header on %q: %w", sheet, err)
			}
		}
		for i, r := range rows {
			cellRow := make([]interface{}, len(r))
			for j, v := range r {
				cellRow[j] = v
			}
			cellRef, err := excelize.CoordinatesToCellName(1, i+2)
			if err != nil {
				return fmt.Errorf("xlsx: ref row %d on %q: %w", i, sheet, err)
			}
			if err := f.SetSheetRow(sheet, cellRef, &cellRow); err != nil {
				return fmt.Errorf("xlsx: write row %d on %q: %w", i, sheet, err)
			}
		}
		return nil
	}

	if len(spec.Content.Sheets) > 0 {
		for _, sh := range spec.Content.Sheets {
			if err := writeSheet(sh.Name, sh.Columns, sh.Rows); err != nil {
				return nil, err
			}
		}
	} else if t := spec.Content.Table; t != nil {
		name := "Sheet1"
		if spec.Title != "" {
			name = truncateSheetName(spec.Title)
		}
		if err := writeSheet(name, t.Columns, t.Rows); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("xlsx: content.sheets or content.table required")
	}

	// Pin the first sheet as the active one in case multi-sheet entry order
	// rearranged things.
	if idx, err := f.GetSheetIndex(f.GetSheetList()[0]); err == nil {
		f.SetActiveSheet(idx)
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("xlsx: write buffer: %w", err)
	}
	return buf.Bytes(), nil
}

// Excel sheet names cap at 31 chars and ban / \ ? * [ ].
func truncateSheetName(s string) string {
	const max = 31
	cleaned := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '/', '\\', '?', '*', '[', ']', ':':
			cleaned = append(cleaned, '_')
		default:
			cleaned = append(cleaned, r)
		}
	}
	if len(cleaned) > max {
		cleaned = cleaned[:max]
	}
	if len(cleaned) == 0 {
		return "Sheet1"
	}
	return string(cleaned)
}
