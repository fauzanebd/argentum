package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeConnRepo implements the slice of domain.ConnectionRepository that
// ResolveSource uses. Every other method panics: if a future change starts
// calling one, the test says so instead of silently exercising a nil repo.
type fakeConnRepo struct {
	byCompany map[string][]*domain.DBConnection
	listErr   error
	listCalls int
}

func (f *fakeConnRepo) ListByCompany(_ context.Context, companyID string) ([]*domain.DBConnection, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.byCompany[companyID], nil
}

func (f *fakeConnRepo) Create(context.Context, *domain.DBConnection) error {
	panic("unexpected Create")
}
func (f *fakeConnRepo) GetByID(context.Context, string) (*domain.DBConnection, error) {
	panic("unexpected GetByID")
}
func (f *fakeConnRepo) GetDefaultForCompany(context.Context, string) (*domain.DBConnection, error) {
	panic("unexpected GetDefaultForCompany")
}
func (f *fakeConnRepo) Update(context.Context, *domain.DBConnection) error {
	panic("unexpected Update")
}
func (f *fakeConnRepo) Delete(context.Context, string) error { panic("unexpected Delete") }
func (f *fakeConnRepo) SetDefault(context.Context, string, string) error {
	panic("unexpected SetDefault")
}

func conn(id, companyID, label, dbType string) *domain.DBConnection {
	return &domain.DBConnection{ID: id, CompanyID: companyID, Label: label, DBType: dbType}
}

// An empty company id must be rejected before the repository is touched. It is
// the tenant-isolation guard: a repo query with "" would either error later or,
// worse, match rows that belong to nobody in particular.
func TestResolveSourceRejectsAnEmptyCompanyID(t *testing.T) {
	repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{
		"": {conn("src-1", "", "orphan", "postgres")},
	}}

	got, err := ResolveSource(context.Background(), repo, "", "src-1")
	if err == nil {
		t.Fatalf("ResolveSource with no company id returned %+v, want an error", got)
	}
	if !strings.Contains(err.Error(), "companyID") {
		t.Errorf("err = %q, want it to name companyID", err)
	}
	if repo.listCalls != 0 {
		t.Errorf("the repository was queried %d times with an empty company id, want 0", repo.listCalls)
	}
}

func TestResolveSourceNoConnections(t *testing.T) {
	repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{}}

	_, err := ResolveSource(context.Background(), repo, "co-1", "")
	if err == nil {
		t.Fatal("ResolveSource = nil error with no registered sources")
	}
	// The agent reads this string and relays it, so it has to tell the user
	// what to do rather than name an internal condition.
	if !strings.Contains(err.Error(), "connect a database in settings") {
		t.Errorf("err = %q, want it to tell the user to connect a database", err)
	}
}

func TestResolveSourceSingleConnection(t *testing.T) {
	only := conn("src-1", "co-1", "warehouse", "postgres")
	repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{"co-1": {only}}}

	t.Run("no id requested", func(t *testing.T) {
		got, err := ResolveSource(context.Background(), repo, "co-1", "")
		if err != nil {
			t.Fatalf("ResolveSource: %v", err)
		}
		if got != only {
			t.Errorf("got %+v, want the only source", got)
		}
	})

	t.Run("the matching id requested", func(t *testing.T) {
		got, err := ResolveSource(context.Background(), repo, "co-1", "src-1")
		if err != nil {
			t.Fatalf("ResolveSource: %v", err)
		}
		if got != only {
			t.Errorf("got %+v, want the only source", got)
		}
	})

	t.Run("a foreign id requested", func(t *testing.T) {
		// The id of another tenant's source must not resolve just because
		// this company happens to have exactly one connection.
		_, err := ResolveSource(context.Background(), repo, "co-1", "src-other")
		if err == nil {
			t.Fatal("ResolveSource = nil error for an id this company does not own")
		}
		if !strings.Contains(err.Error(), "src-other") {
			t.Errorf("err = %q, want it to quote the requested id", err)
		}
	})
}

func TestResolveSourceMultipleConnections(t *testing.T) {
	sales := conn("src-sales", "co-1", "Sales DW", "postgres")
	ops := conn("src-ops", "co-1", "Ops", "mysql")
	repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{"co-1": {sales, ops}}}

	t.Run("no id is an error that lists the menu", func(t *testing.T) {
		_, err := ResolveSource(context.Background(), repo, "co-1", "")
		if err == nil {
			t.Fatal("ResolveSource = nil error with two sources and no source_id")
		}
		// The agent's only route out of this error is to retry with an id, so
		// the ids have to be in the message it gets back.
		for _, want := range []string{"src-sales", "src-ops", "Sales DW", "Ops", "postgres", "mysql"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q, want it to mention %q", err, want)
			}
		}
	})

	t.Run("an explicit id selects", func(t *testing.T) {
		got, err := ResolveSource(context.Background(), repo, "co-1", "src-ops")
		if err != nil {
			t.Fatalf("ResolveSource: %v", err)
		}
		if got != ops {
			t.Errorf("got %+v, want the ops source", got)
		}
	})

	t.Run("an unknown id lists the menu too", func(t *testing.T) {
		_, err := ResolveSource(context.Background(), repo, "co-1", "src-nope")
		if err == nil {
			t.Fatal("ResolveSource = nil error for an unknown id")
		}
		if !strings.Contains(err.Error(), "src-sales") || !strings.Contains(err.Error(), "src-ops") {
			t.Errorf("err = %q, want the available sources listed", err)
		}
	})
}

func TestResolveSourceCrossTenantIDIsNotResolvable(t *testing.T) {
	// The scoping is the ListByCompany call, not a filter afterwards. This
	// pins that an id belonging to another company is invisible even when it
	// exists in the repository.
	mine := conn("src-mine", "co-1", "Mine", "postgres")
	theirs := conn("src-theirs", "co-2", "Theirs", "postgres")
	repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{
		"co-1": {mine},
		"co-2": {theirs},
	}}

	got, err := ResolveSource(context.Background(), repo, "co-1", "src-theirs")
	if err == nil {
		t.Fatalf("ResolveSource resolved another tenant's source: %+v", got)
	}
	if strings.Contains(err.Error(), "Theirs") {
		t.Errorf("err = %q leaks the other tenant's label", err)
	}
}

func TestResolveSourceWrapsARepositoryError(t *testing.T) {
	sentinel := errors.New("connection refused")
	repo := &fakeConnRepo{listErr: sentinel}

	_, err := ResolveSource(context.Background(), repo, "co-1", "")
	if err == nil {
		t.Fatal("ResolveSource = nil error when the repository failed")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want it to wrap the repository error", err)
	}
}

func TestFormatSourceMenuLabelsTheUnlabelled(t *testing.T) {
	// A blank label would render as "src-1= [postgres]", which reads like a
	// truncated message rather than a source with no name.
	got := formatSourceMenu([]*domain.DBConnection{
		conn("src-1", "co-1", "", "postgres"),
		conn("src-2", "co-1", "Ops", "mysql"),
	})
	if !strings.Contains(got, "(unlabelled)") {
		t.Errorf("menu = %q, want the unlabelled source marked", got)
	}
	if !strings.Contains(got, "src-2=Ops [mysql]") {
		t.Errorf("menu = %q, want id=label [type] for a labelled source", got)
	}
	if !strings.Contains(got, ", ") {
		t.Errorf("menu = %q, want the entries comma-separated", got)
	}
}
