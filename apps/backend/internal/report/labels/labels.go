// Package labels is the handful of words a renderer contributes to a document
// it did not write: the cover's fact labels, the footer's timestamp prefix, the
// page counter, and the marker on a slide that continues the one before it.
//
// They follow the document's locale rather than the process's. An Indonesian
// report whose figures read "Rp 3.863.405.700" and whose footer reads "Page 2
// of 3" is a document assembled by a tool that was not paying attention, and it
// is the kind of detail a reader notices before they notice anything the report
// says.
//
// This is not i18n and should not grow into it: the model writes every other
// word in the document in whatever language the conversation is in. These exist
// because they are the only ones a renderer chooses — and they are shared
// between the PDF and the deck because the same document rendered both ways
// should not say "Prepared for" on one and "Disiapkan untuk" on the other.
package labels

import "github.com/fauzanebd/argentum/internal/report/format"

type Set struct {
	PreparedFor string
	PreparedBy  string
	Generated   string

	// PageNumber is a maroto pattern, with {current} and {total} substituted as
	// the document is written — the total is not known until every page exists.
	PageNumber string

	// Continued marks a slide carrying the overflow of the one before it. The
	// deck has no page-fitting engine to consult, so the alternative to saying
	// this out loud is silent clipping.
	Continued string

	// Notes titles the speaker-notes block when the renderer has to introduce
	// it, e.g. the source line under a table.
	Source string
}

func For(loc format.Locale) Set {
	if loc == format.LocaleID {
		return Set{
			PreparedFor: "Disiapkan untuk",
			PreparedBy:  "Disiapkan oleh",
			Generated:   "Dibuat",
			PageNumber:  "Halaman {current} dari {total}",
			Continued:   "(lanjutan)",
			Source:      "Sumber",
		}
	}
	return Set{
		PreparedFor: "Prepared for",
		PreparedBy:  "Prepared by",
		Generated:   "Generated",
		PageNumber:  "Page {current} of {total}",
		Continued:   "(cont.)",
		Source:      "Source",
	}
}
