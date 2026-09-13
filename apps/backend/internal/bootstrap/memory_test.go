package bootstrap

import (
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/peermemory"
)

// T-N11's wiring. The view is only a fix if the memory every agent is built with
// is behind it, on both of buildMemory's branches — and a wrapper applied at one
// call site is the kind of line a refactor drops without a test noticing.
func TestTheAgentMemoryIsBehindTheRoomViewOnBothBranches(t *testing.T) {
	mr := miniredis.RunT(t)
	for name, cfg := range map[string]*config.Config{
		"redis":                 {RedisURL: mr.Addr()},
		"in-process (no redis)": {},
	} {
		if _, ok := buildMemory(cfg).(*peermemory.Memory); !ok {
			t.Errorf("%s: buildMemory returned %T, not the room view — every agent in a room would read its colleagues' turns as its own",
				name, buildMemory(cfg))
		}
	}
}
