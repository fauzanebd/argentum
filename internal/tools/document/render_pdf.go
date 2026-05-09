package document

import (
	"fmt"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// Maroto's grid is 12 columns wide.
const gridCols = 12

func RenderPDF(spec *Spec) ([]byte, error) {
	m := maroto.New()

	if spec.Title != "" {
		m.AddRow(14, col.New(gridCols).Add(
			text.New(spec.Title, props.Text{
				Size:  16,
				Style: fontstyle.Bold,
				Align: align.Left,
				Top:   2,
			}),
		))
		spacer(m, 2)
	}

	if len(spec.Content.Sections) > 0 {
		for _, sec := range spec.Content.Sections {
			if err := renderSection(m, sec); err != nil {
				return nil, err
			}
		}
	} else if t := spec.Content.Table; t != nil {
		renderTable(m, t.Columns, t.Rows)
	} else {
		return nil, fmt.Errorf("pdf: content.sections or content.table required")
	}

	doc, err := m.Generate()
	if err != nil {
		return nil, fmt.Errorf("pdf: generate: %w", err)
	}
	return doc.GetBytes(), nil
}

func renderSection(m core.Maroto, sec Section) error {
	switch sec.Type {
	case "heading":
		m.AddRow(10, col.New(gridCols).Add(
			text.New(sec.Text, props.Text{
				Size:  13,
				Style: fontstyle.Bold,
				Top:   2,
			}),
		))
	case "paragraph":
		m.AddAutoRow(col.New(gridCols).Add(
			text.New(sec.Text, props.Text{
				Size: 10,
				Top:  1,
			}),
		))
	case "key_value":
		renderKeyValue(m, sec.Items)
	case "table":
		renderTable(m, sec.Columns, sec.Rows)
	case "spacer":
		size := sec.Size
		if size <= 0 {
			size = 4
		}
		spacer(m, size)
	default:
		return fmt.Errorf("pdf: unknown section type %q", sec.Type)
	}
	return nil
}

func renderKeyValue(m core.Maroto, items []KV) {
	for _, kv := range items {
		m.AddRow(6,
			col.New(4).Add(text.New(kv.K, props.Text{
				Size:  10,
				Style: fontstyle.Bold,
				Top:   1,
			})),
			col.New(8).Add(text.New(kv.V, props.Text{
				Size: 10,
				Top:  1,
			})),
		)
	}
}

func renderTable(m core.Maroto, columns []string, rows [][]string) {
	if len(columns) == 0 {
		return
	}
	cols := splitGrid(len(columns))
	n := len(cols) // capped at 12 inside splitGrid

	headerCells := make([]core.Col, 0, n)
	for i := 0; i < n; i++ {
		headerCells = append(headerCells, col.New(cols[i]).Add(
			text.New(columns[i], props.Text{
				Size:  10,
				Style: fontstyle.Bold,
				Align: align.Left,
				Top:   2,
			}),
		))
	}
	m.AddRow(8, headerCells...)

	for _, r := range rows {
		cells := make([]core.Col, 0, n)
		for i := 0; i < n; i++ {
			val := ""
			if i < len(r) {
				val = r[i]
			}
			cells = append(cells, col.New(cols[i]).Add(
				text.New(val, props.Text{
					Size: 9,
					Top:  2,
				}),
			))
		}
		m.AddRow(7, cells...)
	}
}

// splitGrid distributes the 12-col grid across n columns. The last column
// absorbs the remainder so the row always sums to 12. For n > 12 we cap
// at 12 (the renderer skips extra columns); the LLM is told to keep
// tables under ~8 columns for readability anyway.
func splitGrid(n int) []int {
	if n <= 0 {
		return nil
	}
	if n > gridCols {
		n = gridCols
	}
	base := gridCols / n
	out := make([]int, n)
	for i := range out {
		out[i] = base
	}
	out[n-1] += gridCols - base*n
	return out
}

func spacer(m core.Maroto, height float64) {
	m.AddRow(height, col.New(gridCols).Add(text.New("", props.Text{Size: 1})))
}
