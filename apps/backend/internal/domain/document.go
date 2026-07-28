package domain

import (
	"context"
	"time"
)

type DocumentFormat string

const (
	DocumentFormatPDF  DocumentFormat = "pdf"
	DocumentFormatXLSX DocumentFormat = "xlsx"
	DocumentFormatCSV  DocumentFormat = "csv"
	DocumentFormatPPTX DocumentFormat = "pptx"
)

func (f DocumentFormat) Valid() bool {
	switch f {
	case DocumentFormatPDF, DocumentFormatXLSX, DocumentFormatCSV, DocumentFormatPPTX:
		return true
	}
	return false
}

func (f DocumentFormat) Extension() string {
	switch f {
	case DocumentFormatPDF:
		return "pdf"
	case DocumentFormatXLSX:
		return "xlsx"
	case DocumentFormatCSV:
		return "csv"
	case DocumentFormatPPTX:
		return "pptx"
	}
	return ""
}

func (f DocumentFormat) ContentType() string {
	switch f {
	case DocumentFormatPDF:
		return "application/pdf"
	case DocumentFormatXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case DocumentFormatCSV:
		return "text/csv; charset=utf-8"
	case DocumentFormatPPTX:
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	}
	return "application/octet-stream"
}

// Document is a persisted artifact produced by the generate_document tool.
// Generic-purpose: invoices, agreements, T&Cs, research summaries, exports.
type Document struct {
	ID         string         `json:"id"`
	CompanyID  string         `json:"company_id"`
	ThreadID   string         `json:"thread_id"`
	MessageID  string         `json:"message_id,omitempty"`
	Format     DocumentFormat `json:"format"`
	Filename   string         `json:"filename"`
	StorageKey string         `json:"storage_key"`
	SizeBytes  int64          `json:"size_bytes"`
	CreatedAt  time.Time      `json:"created_at"`
}

// DocumentRepository persists document metadata.
type DocumentRepository interface {
	Insert(ctx context.Context, d *Document) error
	GetByID(ctx context.Context, id string) (*Document, error)
	ListByThread(ctx context.Context, threadID string) ([]*Document, error)
}
