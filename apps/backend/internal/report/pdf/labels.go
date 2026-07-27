package pdf

import "github.com/fauzanebd/argentum/internal/report/format"

// The handful of words the renderer contributes to a document it did not
// write: the cover's fact labels, the footer's timestamp prefix, and the page
// counter.
//
// They follow the document's locale rather than the process's. An Indonesian
// report whose figures read "Rp 3.863.405.700" and whose footer reads
// "Page 2 of 3" is a document assembled by a tool that was not paying
// attention, and it is the kind of detail a reader notices before they notice
// anything the report says.
//
// This is not i18n and should not grow into it: the model writes every other
// word in the document in whatever language the conversation is in. These four
// strings exist because they are the only ones the renderer chooses.
type labels struct {
	preparedFor string
	preparedBy  string
	generated   string
	pageNumber  string
}

func labelsFor(loc format.Locale) labels {
	if loc == format.LocaleID {
		return labels{
			preparedFor: "Disiapkan untuk",
			preparedBy:  "Disiapkan oleh",
			generated:   "Dibuat",
			pageNumber:  "Halaman {current} dari {total}",
		}
	}
	return labels{
		preparedFor: "Prepared for",
		preparedBy:  "Prepared by",
		generated:   "Generated",
		pageNumber:  "Page {current} of {total}",
	}
}
