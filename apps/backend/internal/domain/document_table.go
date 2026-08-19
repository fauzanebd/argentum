package domain

import (
	"context"
	"time"

	"github.com/fauzanebd/argentum/internal/doctable"
)

// DocumentTable is one table found inside an uploaded PDF, and how far it has
// got towards being data (T-P4 → T-P6).
//
// **It is a draft until a person applies it.** The precedent is [SourceProfile]
// — *"an inferred profile that silently became the agent's view of the business
// would be a fabrication with a UI"* — and a table inferred out of a PDF is the
// stronger version of the same hazard, because it does not read as an opinion.
// It reads as data. So this row exists in the control database holding what was
// extracted and what a reviewer decided about it, and the rows themselves reach
// the document warehouse only when [DocumentTableApplied] is written.
//
// **The extracted rows are not stored here, on purpose.** They live in the page
// artifacts the parse wrote, and every read re-derives them through
// `doctable.Build` — deterministic code over immutable objects. Two things
// follow, and both are wanted: a better parser improves every draft nobody has
// applied yet, and the control database does not grow a copy of every document
// a tenant ever uploaded.
type DocumentTable struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	CompanyID  string `json:"company_id"`
	// Title is what the reviewer named it, defaulted from the caption above the
	// grid. It is what the review surface lists and what `list_sources`
	// eventually says the source holds.
	Title string `json:"title"`
	// TableName is the identifier in the warehouse schema. Slugified, unique
	// per company, and legible — it reaches a model through `get_schema`.
	TableName string `json:"table_name"`
	FirstPage int    `json:"first_page"`
	LastPage  int    `json:"last_page"`
	// Columns is the typing decision: inferred by `internal/doctable` and then
	// edited by the reviewer. What is stored is the decision, not the data.
	Columns []doctable.Column   `json:"columns"`
	Status  DocumentTableStatus `json:"status"`
	// VerifyStatus and VerifyDetail are T-P5's outcome, re-computed on every
	// read and persisted so a list can show the badge without re-deriving every
	// table on the page.
	VerifyStatus doctable.VerifyStatus `json:"verify_status"`
	VerifyDetail string                `json:"verify_detail,omitempty"`
	RowCount     int                   `json:"row_count"`
	// CandidateKey is `p<first page>-c<candidate index>`, which is what makes a
	// re-parse update the draft a reviewer has been editing rather than add a
	// second one beside it.
	CandidateKey string     `json:"candidate_key"`
	AppliedBy    string     `json:"applied_by,omitempty"`
	AppliedAt    *time.Time `json:"applied_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// DocumentTableStatus is how far this table has got.
type DocumentTableStatus string

const (
	// DocumentTableDraft is extracted and not published. Every table starts
	// here and most stay here, which is the design working rather than failing.
	DocumentTableDraft DocumentTableStatus = "draft"
	// DocumentTableApplied means the rows are in the warehouse and the agent
	// can query them.
	DocumentTableApplied DocumentTableStatus = "applied"
	// DocumentTableQuarantined means the arithmetic check refused it. Kept and
	// shown, never published — the stated total may be the misparsed value, so
	// the only correct output is a person looking at the page.
	DocumentTableQuarantined DocumentTableStatus = "quarantined"
)

// String is for log lines and error sentences, which are the two places a
// status is written next to prose rather than serialised.
func (s DocumentTableStatus) String() string { return string(s) }

// DocumentTableRepository persists the drafts.
//
// Scoped by company in the query rather than in the caller's memory, for the
// reason [SourceDocumentRepository] states: an id from another tenant has to be
// a not-found, and a handler that fetches first and compares afterwards is one
// forgotten comparison away from a cross-tenant read.
type DocumentTableRepository interface {
	// Upsert writes a draft, matching on (document_id, candidate_key). A
	// re-parse of the same document therefore updates the draft a reviewer has
	// been editing instead of duplicating it — and it leaves an applied table
	// applied, because re-reading a document is not a decision to unpublish
	// what somebody already reviewed.
	Upsert(ctx context.Context, t *DocumentTable) error
	GetForCompany(ctx context.Context, companyID, id string) (*DocumentTable, error)
	ListByDocument(ctx context.Context, companyID, documentID string) ([]*DocumentTable, error)
	// ListAppliedByCompany is what the source list and the delete path read.
	ListAppliedByCompany(ctx context.Context, companyID string) ([]*DocumentTable, error)
	// UpdateColumns is the reviewer's edit: the typing decision and the title,
	// and nothing else. Status is moved by Apply, and letting an edit carry a
	// status would make "save my column change" a path that can publish.
	UpdateColumns(ctx context.Context, companyID, id string, title string, cols []doctable.Column) error
	// MarkApplied records the publish: who, when, how many rows, and the
	// verification that was true at the time.
	MarkApplied(ctx context.Context, companyID, id, userID string, rowCount int) error
	// SetVerification persists what the arithmetic check concluded on the last
	// derivation, and moves the status to quarantined or back off it.
	SetVerification(ctx context.Context, companyID, id string, status doctable.VerifyStatus, detail string) error
	Delete(ctx context.Context, companyID, id string) error
}
