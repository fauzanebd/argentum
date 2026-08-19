package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/doctable"
	"github.com/fauzanebd/argentum/internal/docwarehouse"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DocumentTableService is the path from a parsed document to a queryable source
// (T-P4 → T-P6).
//
// **The rows are never stored in the control database.** Every read re-derives
// them from the page artifacts the parse wrote, through `doctable.Build`, which
// is deterministic code over immutable objects. Three things follow and all
// three are wanted: a better parser improves every draft nobody applied yet,
// the control database does not accumulate a copy of every uploaded document,
// and a reviewer's column decision is a small JSON blob rather than a
// re-materialization of the data.
//
// **Publishing is a decision, not a step.** `Apply` is the only method that
// writes to the warehouse, it takes the user who pressed the button, and it
// refuses a quarantined table. The roadmap's Decision 3 is the argument: an
// extraction that silently became the agent's view of the business would be a
// fabrication with a UI, and this is the surface where a person prevents that.
type DocumentTableService struct {
	docs   domain.SourceDocumentRepository
	tables domain.DocumentTableRepository
	blobs  DocumentArtifactStore
	// warehouse is nil on a deployment without DOC_WAREHOUSE_DSN. Reviewing
	// still works there; publishing refuses with a sentence, which is the
	// behaviour the ticket asks for and the opposite of falling back to the
	// control database.
	warehouse *docwarehouse.Warehouse
	conns     domain.ConnectionRepository
	cipher    DSNEncryptor
	pool      ConnectionInvalidator
	// schemaCache is the get_schema cache, dropped on every publish and every
	// drop. See WithWarehouse for why it is not optional.
	schemaCache ConnectionInvalidator
}

// DSNEncryptor is the half of crypto.DSNCipher this service needs: it stores a
// warehouse DSN the same way a tenant's own is stored, because it is the same
// kind of secret and the agent reaches it through the same pool.
type DSNEncryptor interface {
	Encrypt(plain string) ([]byte, error)
}

// ConnectionInvalidator drops a cached connection so the next turn re-dials.
//
// Declared here rather than taking `*db.Pool` for the reason CookbookService
// states about its embedder: the concrete pool dials on construction, and what
// needs exercising in a test is the ordering around a rotated credential.
type ConnectionInvalidator interface {
	Invalidate(companyID, connectionID string)
}

// ErrDocumentNotParsed is returned when a document has no artifacts to read.
// A distinct error because the remedy is different from every other failure
// here: wait, or queue the parse — not "fix the table".
var ErrDocumentNotParsed = errors.New("this document has not been parsed yet")

// ErrTableQuarantined is the refusal T-P5 exists to produce.
var ErrTableQuarantined = errors.New("this table did not add up and cannot be published")

// ErrWarehouseUnavailable is publishing on a deployment with no document
// warehouse.
var ErrWarehouseUnavailable = docwarehouse.ErrNotConfigured

func NewDocumentTableService(
	docs domain.SourceDocumentRepository,
	tables domain.DocumentTableRepository,
	blobs DocumentArtifactStore,
) *DocumentTableService {
	return &DocumentTableService{docs: docs, tables: tables, blobs: blobs}
}

// WithWarehouse enables publishing. Passing a nil warehouse leaves the service
// review-only, which is a supported deployment rather than a broken one.
// schemaCache is required rather than optional on purpose. Publishing changes
// what the document source contains, and a schema cache that still holds the
// pre-publish answer tells the agent an applied table does not exist —
// found live on 2026-08-19, where it survived for a full cacheTTL. An
// optional setter is how a dependency gets forgotten; this one cannot be
// wired late, because a deployment that can publish is exactly a deployment
// that must invalidate.
func (s *DocumentTableService) WithWarehouse(
	w *docwarehouse.Warehouse, conns domain.ConnectionRepository,
	cipher DSNEncryptor, pool ConnectionInvalidator, schemaCache ConnectionInvalidator,
) *DocumentTableService {
	s.warehouse = w
	s.conns = conns
	s.cipher = cipher
	s.pool = pool
	s.schemaCache = schemaCache
	return s
}

// invalidateDocumentSource drops the cached connection and the cached schema
// for this company's document source.
//
// Called by every path that changes what the warehouse holds — publish,
// unpublish and delete — because each of them makes the cached schema a
// statement about a warehouse that no longer exists. `company_service` has done
// both halves together since a tenant could rotate a DSN; this is the same pair
// for the source this product builds itself.
func (s *DocumentTableService) invalidateDocumentSource(ctx context.Context, companyID string) {
	if s.conns == nil {
		return
	}
	conns, err := s.conns.ListByCompany(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("could not list sources to invalidate the document schema cache")
		return
	}
	for _, c := range conns {
		if c.Origin != domain.OriginDocument {
			continue
		}
		if s.pool != nil {
			s.pool.Invalidate(companyID, c.ID)
		}
		if s.schemaCache != nil {
			s.schemaCache.Invalidate(companyID, c.ID)
		}
	}
}

// DraftTable is one extracted table as the review surface needs it: the stored
// decision, and the data that decision applies to.
type DraftTable struct {
	*domain.DocumentTable
	// Table is the re-derived extraction — rows, totals, page rectangles and
	// notes. It is the half a reviewer looks at and it is never persisted.
	Table doctable.Table `json:"table"`
}

// List returns every table in a document, drafts reconciled with what the
// parser currently finds.
//
// Reconciliation happens on read rather than at the end of the parse, and that
// is a deliberate trade. It costs a re-derivation per open, which is arithmetic
// over a handful of JSON objects; it buys a review surface that is never stale
// with respect to the parser, and a parse worker that does not need to know
// about drafts, reviewers or the control database beyond the status column it
// already writes.
func (s *DocumentTableService) List(ctx context.Context, companyID, documentID string) ([]DraftTable, error) {
	doc, err := s.docs.GetForCompany(ctx, companyID, documentID)
	if err != nil {
		return nil, err
	}
	derived, err := s.derive(ctx, doc)
	if err != nil {
		return nil, err
	}
	stored, err := s.tables.ListByDocument(ctx, companyID, documentID)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*domain.DocumentTable, len(stored))
	for _, t := range stored {
		byKey[t.CandidateKey] = t
	}

	out := make([]DraftTable, 0, len(derived))
	for _, table := range derived {
		key := table.Key()
		row := byKey[key]
		if row == nil {
			row = &domain.DocumentTable{
				DocumentID:   doc.ID,
				CompanyID:    companyID,
				Title:        defaultTitle(table, doc, len(out)+1),
				TableName:    docwarehouse.Identifier(defaultTitle(table, doc, len(out)+1)),
				CandidateKey: key,
				Status:       domain.DocumentTableDraft,
				Columns:      table.Columns,
			}
		} else if len(row.Columns) > 0 {
			// A reviewer's decision, re-applied to freshly derived rows. This is
			// the line that makes a type override mean something after the
			// document is re-parsed.
			table = doctable.Revalue(table, row.Columns)
		}

		row.FirstPage = table.FirstPage
		row.LastPage = table.LastPage
		row.RowCount = len(table.Rows)
		row.VerifyStatus = table.Verify.Status
		row.VerifyDetail = table.Verify.Detail
		if err := s.upsertWithFreeName(ctx, row); err != nil {
			return nil, fmt.Errorf("record document table draft: %w", err)
		}
		out = append(out, DraftTable{DocumentTable: row, Table: table})
	}
	return out, nil
}

// upsertWithFreeName writes the draft, working around a table name another
// document already holds.
//
// Two documents each holding a "penjualan" table is the ordinary case in a
// tenant that uploads a report every month — not a conflict a person should be
// asked to resolve. The suffix is numeric and stops at a bound, because a loop
// that renames forever is a loop that hides a real problem: twenty tables
// called the same thing means the naming rule, not the tenant, is wrong.
func (s *DocumentTableService) upsertWithFreeName(ctx context.Context, row *domain.DocumentTable) error {
	base := row.TableName
	for attempt := 1; attempt <= 20; attempt++ {
		err := s.tables.Upsert(ctx, row)
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return err
		}
		row.TableName = fmt.Sprintf("%s_%d", base, attempt+1)
	}
	return fmt.Errorf("could not find a free table name for %q", base)
}

// Get is one table with its rows, for the detail view and for Apply.
func (s *DocumentTableService) Get(ctx context.Context, companyID, tableID string) (*DraftTable, error) {
	row, err := s.tables.GetForCompany(ctx, companyID, tableID)
	if err != nil {
		return nil, err
	}
	doc, err := s.docs.GetForCompany(ctx, companyID, row.DocumentID)
	if err != nil {
		return nil, err
	}
	derived, err := s.derive(ctx, doc)
	if err != nil {
		return nil, err
	}
	for _, table := range derived {
		if table.Key() != row.CandidateKey {
			continue
		}
		if len(row.Columns) > 0 {
			table = doctable.Revalue(table, row.Columns)
		}
		return &DraftTable{DocumentTable: row, Table: table}, nil
	}
	// The draft names a candidate the current parser no longer produces. Said
	// plainly rather than answered with an empty table: the reviewer's decision
	// is about a table that is not there any more, and quietly showing them a
	// different one is the worst available answer.
	return nil, fmt.Errorf("%w: the parser no longer finds this table in the document", domain.ErrNotFound)
}

// UpdateColumns saves a reviewer's typing decision and re-runs the arithmetic
// check under it.
//
// Re-verifying here is the point. A reviewer who corrects a multiplier changes
// every value in the column, which changes what the stated total is being
// compared against — so a table that was quarantined can become publishable,
// and one that was verified can stop being. Persisting the old verdict beside
// the new types would be an instrument reporting on data that no longer exists.
func (s *DocumentTableService) UpdateColumns(
	ctx context.Context, companyID, tableID, title string, cols []doctable.Column,
) (*DraftTable, error) {
	row, err := s.tables.GetForCompany(ctx, companyID, tableID)
	if err != nil {
		return nil, err
	}
	if row.Status == domain.DocumentTableApplied {
		// Editing an applied table would leave the warehouse holding rows typed
		// by a decision nobody can see any more. Re-applying is the path, and it
		// is one button away.
		return nil, fmt.Errorf("%w: unpublish this table before changing its columns", domain.ErrInvalidInput)
	}
	if err := s.tables.UpdateColumns(ctx, companyID, tableID, title, cols); err != nil {
		return nil, err
	}
	draft, err := s.Get(ctx, companyID, tableID)
	if err != nil {
		return nil, err
	}
	if err := s.tables.SetVerification(ctx, companyID, tableID,
		draft.Table.Verify.Status, draft.Table.Verify.Detail); err != nil {
		return nil, err
	}
	draft.VerifyStatus = draft.Table.Verify.Status
	draft.VerifyDetail = draft.Table.Verify.Detail
	return draft, nil
}

// Apply publishes one table into the document warehouse.
//
// The order is: derive, re-verify, refuse if quarantined, ensure the tenant's
// schema and source, replace the table, then record the publish. Recording last
// is the only order that fails safely — a row saying `applied` over a warehouse
// table that was never written is a source the agent will query and find
// nothing in, and "nothing" is an answer this product's whole instrument stack
// is bad at contradicting.
func (s *DocumentTableService) Apply(ctx context.Context, companyID, tableID, userID string) (*DraftTable, error) {
	if s.warehouse == nil || !s.warehouse.Configured() {
		return nil, ErrWarehouseUnavailable
	}
	draft, err := s.Get(ctx, companyID, tableID)
	if err != nil {
		return nil, err
	}
	if !draft.Table.Verify.Publishable() {
		// Checked here as well as in the SQL, because the sentence a person
		// reads should name the mismatch rather than say "not found".
		return nil, fmt.Errorf("%w: %s", ErrTableQuarantined, draft.Table.Verify.Detail)
	}
	if len(draft.Table.Rows) == 0 {
		return nil, fmt.Errorf("%w: this table has no data rows", domain.ErrInvalidInput)
	}

	tenant, err := s.warehouse.EnsureTenant(ctx, companyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.warehouse.Replace(ctx, tenant.Schema, draft.TableName,
		draft.Table.Columns, draft.Table.Rows)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSource(ctx, companyID, tenant); err != nil {
		return nil, err
	}
	if err := s.tables.MarkApplied(ctx, companyID, tableID, userID, rows); err != nil {
		return nil, err
	}
	// The table exists now; the cached schema says it does not.
	s.invalidateDocumentSource(ctx, companyID)

	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"table_id":   tableID,
		"table":      draft.TableName,
		"rows":       rows,
		"verify":     draft.Table.Verify.Status,
		"applied_by": userID,
	}).Info("document table published")

	draft.Status = domain.DocumentTableApplied
	draft.RowCount = rows
	now := time.Now().UTC()
	draft.AppliedAt = &now
	draft.AppliedBy = userID
	return draft, nil
}

// Unpublish drops a published table out of the warehouse and returns the draft
// to editable. The row stays: what was extracted and what a reviewer decided is
// worth keeping even when the data is withdrawn.
func (s *DocumentTableService) Unpublish(ctx context.Context, companyID, tableID string) error {
	if s.warehouse == nil || !s.warehouse.Configured() {
		return ErrWarehouseUnavailable
	}
	row, err := s.tables.GetForCompany(ctx, companyID, tableID)
	if err != nil {
		return err
	}
	if err := s.warehouse.Drop(ctx, docwarehouse.SchemaName(companyID), row.TableName); err != nil {
		return err
	}
	s.invalidateDocumentSource(ctx, companyID)
	// SetVerification is what moves the status off `applied`: it recomputes to
	// draft or quarantined from the verification, which is exactly the state
	// this table should return to.
	return s.tables.SetVerification(ctx, companyID, tableID, row.VerifyStatus, row.VerifyDetail)
}

// DropForDocument removes every warehouse table a document published. Called by
// the delete path, which owes four removals — the row, the chunks, the object
// and this (T-P12).
func (s *DocumentTableService) DropForDocument(ctx context.Context, companyID, documentID string) error {
	if s.warehouse == nil || !s.warehouse.Configured() {
		return nil
	}
	rows, err := s.tables.ListByDocument(ctx, companyID, documentID)
	if err != nil {
		return err
	}
	schema := docwarehouse.SchemaName(companyID)
	dropped := 0
	for _, row := range rows {
		if row.Status != domain.DocumentTableApplied {
			continue
		}
		if err := s.warehouse.Drop(ctx, schema, row.TableName); err != nil {
			return err
		}
		dropped++
	}
	if dropped > 0 {
		s.invalidateDocumentSource(ctx, companyID)
	}
	return nil
}

// ensureSource makes sure the company has a `db_connections` row pointing at
// its document schema, and that the row holds the credentials the warehouse
// just issued.
//
// The DSN is rewritten on every publish because [docwarehouse.Warehouse.EnsureTenant]
// rotates the role's password. The pool is invalidated after the write so the
// next turn re-dials with them — without that, the agent holds a connection
// authenticated with a password that no longer exists and finds out at the
// least convenient moment, which is the middle of somebody's question.
func (s *DocumentTableService) ensureSource(ctx context.Context, companyID string, tenant *docwarehouse.Tenant) error {
	if s.conns == nil || s.cipher == nil {
		return fmt.Errorf("document sources are not wired on this deployment")
	}
	enc, err := s.cipher.Encrypt(tenant.DSN)
	if err != nil {
		return fmt.Errorf("encrypt document warehouse dsn: %w", err)
	}

	existing, err := s.conns.ListByCompany(ctx, companyID)
	if err != nil {
		return fmt.Errorf("list sources: %w", err)
	}
	for _, c := range existing {
		if c.Origin != domain.OriginDocument {
			continue
		}
		c.DSNEncrypted = enc
		c.Description = documentSourceDescription
		if err := s.conns.Update(ctx, c); err != nil {
			return fmt.Errorf("update document source: %w", err)
		}
		if s.pool != nil {
			s.pool.Invalidate(companyID, c.ID)
		}
		return nil
	}

	conn := &domain.DBConnection{
		CompanyID:    companyID,
		DBType:       "postgres",
		Label:        "Uploaded documents",
		Description:  documentSourceDescription,
		DSNEncrypted: enc,
		Origin:       domain.OriginDocument,
		// Never the default. A tenant's own warehouse is what a bare question
		// is about; a document source answers questions about documents, and
		// making it the default would silently re-point every existing turn.
		IsDefault:         false,
		DescriptionSource: domain.DescriptionSourceManual,
	}
	if err := s.conns.Create(ctx, conn); err != nil {
		return fmt.Errorf("create document source: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"company_id":    companyID,
		"connection_id": conn.ID,
		"schema":        tenant.Schema,
	}).Info("document source registered")
	return nil
}

// documentSourceDescription is what `list_sources` tells the agent this source
// is. It says "extracted from uploaded PDFs" in as many words, because the
// agent's choice between this and the tenant's warehouse should be informed by
// the fact that one of them is derived.
const documentSourceDescription = "Tables extracted from PDFs this workspace uploaded and a person reviewed. " +
	"Every row carries source_page and source_row, naming the page it was read from."

// derive rebuilds the tables in a document from its page artifacts.
func (s *DocumentTableService) derive(ctx context.Context, doc *domain.SourceDocument) ([]doctable.Table, error) {
	if doc.Status != domain.SourceDocumentParsed || doc.PageCount == 0 {
		return nil, fmt.Errorf("%w: it is %s", ErrDocumentNotParsed, doc.Status)
	}
	pages := make([]docparse.Page, 0, doc.PageCount)
	for n := 1; n <= doc.PageCount; n++ {
		key := PageArtifactKey(doc.CompanyID, doc.ContentSHA256, n)
		body, err := s.blobs.DownloadKey(ctx, key)
		if err != nil {
			// One unreadable page does not cost the other forty their tables —
			// the same rule the parse service follows for a page that failed to
			// parse. What it does cost is any table that spans it, which is why
			// this is logged rather than silent.
			logrus.WithError(err).WithFields(logrus.Fields{
				"document_id": doc.ID, "page": n,
			}).Warn("page artifact could not be read; its tables are missing from the review")
			continue
		}
		var page docparse.Page
		if err := json.Unmarshal(body, &page); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"document_id": doc.ID, "page": n,
			}).Warn("page artifact could not be decoded; its tables are missing from the review")
			continue
		}
		pages = append(pages, page)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: no page artifacts could be read", ErrDocumentNotParsed)
	}
	return doctable.Build(pages, doctable.Options{MinRows: 2}), nil
}

// defaultTitle names a table before a reviewer does.
//
// The caption first, because a document that titled its own table said what it
// is better than any rule here could. The filename second, because "laporan-q4"
// is at least about the right subject. The position last, because a name has to
// exist — a table called nothing is a table nobody can find in a source list.
func defaultTitle(t doctable.Table, doc *domain.SourceDocument, position int) string {
	if title := strings.TrimSpace(t.Title); title != "" {
		return title
	}
	base := strings.TrimSuffix(doc.Filename, ".pdf")
	if base = strings.TrimSpace(base); base != "" {
		return fmt.Sprintf("%s table %d", base, position)
	}
	return fmt.Sprintf("Table %d, page %d", position, t.FirstPage)
}
