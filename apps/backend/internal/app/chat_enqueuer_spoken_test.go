package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-W9: the record the handler checked is written onto the user message the
// enqueuer appends, under the voice key — and a typed message carries no
// metadata at all, exactly as before the microphone existed.
func TestEnqueueWritesASpokenQuestionOntoTheUserMessage(t *testing.T) {
	f := newAccessFixture(t, "ag-fin", nil, dashboardThread("th-1", "ag-fin"))
	ctx := context.Background()

	spoken := onThread("th-1", "berapa stok gudang barat")
	spoken.Spoken = &domain.SpokenQuestion{ClipIDs: []string{"c-1"}, Verbatim: true}
	if _, err := f.enq.Enqueue(ctx, spoken); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if f.msgs.last == nil || f.msgs.last.Role != domain.MessageRoleUser {
		t.Fatalf("appended %+v, want the user message", f.msgs.last)
	}
	got, ok := f.msgs.last.Metadata[domain.MessageMetadataVoice].(*domain.SpokenQuestion)
	if !ok || !reflect.DeepEqual(got, spoken.Spoken) {
		t.Errorf("metadata = %#v, want voice: %+v", f.msgs.last.Metadata, spoken.Spoken)
	}

	if _, err := f.enq.Enqueue(ctx, onThread("th-1", "dan minggu lalu?")); err != nil {
		t.Fatalf("enqueue typed: %v", err)
	}
	if f.msgs.last.Metadata != nil {
		t.Errorf("a typed message was written with metadata %#v", f.msgs.last.Metadata)
	}
}
