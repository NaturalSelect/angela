package chat

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

// BenchmarkLoadSession reproduces what opening a session does before its
// transcript shows, plus the warm-up pass that follows. Each variant reports
// the time spent reading the transcript, resolving nested agent calls,
// building chat items and rendering every item once, per iteration.
//
// Point ANGELA_BENCH_SESSION at a full session id and ANGELA_BENCH_DATADIR at
// the angela data dir (defaults to ./.angela).
//
//	ANGELA_BENCH_SESSION=1048c867-bc96-4bea-b44f-ba616d239999 \
//	ANGELA_BENCH_DATADIR=/tmp/angela-bench-dm \
//	  go test ./internal/ui/chat/ -run '^$' -bench BenchmarkLoadSession -benchtime 1x -v
func BenchmarkLoadSession(b *testing.B) {
	sessionID := os.Getenv("ANGELA_BENCH_SESSION")
	if sessionID == "" {
		b.Skip("set ANGELA_BENCH_SESSION to a full session id")
	}
	dataDir := os.Getenv("ANGELA_BENCH_DATADIR")
	if dataDir == "" {
		dataDir = ".angela"
	}

	ctx := context.Background()
	conn, err := db.Connect(ctx, dataDir, db.WithDataDirLock(false))
	if err != nil {
		b.Fatalf("connect: %v", err)
	}
	// NOTE: Intentionally not closing conn. db.Connect pools connections by
	// path, and the testing framework may invoke this function more than
	// once; closing would break the shared pooled *sql.DB on re-entry.

	svc := message.NewService(db.New(conn))
	sty := styles.CharmtonePantera()

	b.Run("full", func(b *testing.B) {
		benchLoad(b, ctx, svc, &sty, func() ([]message.Message, error) {
			return svc.List(ctx, sessionID)
		})
	})

	// NOTE: 200 is the page size the UI reads on open. It is repeated here
	// because the UI package imports this one.
	b.Run("tail200", func(b *testing.B) {
		benchLoad(b, ctx, svc, &sty, func() ([]message.Message, error) {
			page, err := svc.ListPage(ctx, sessionID, "", 200)
			return page.Messages, err
		})
	})
}

// benchLoad runs the open-session pipeline once per iteration against the
// messages fetch returns and reports the cost of each stage.
func benchLoad(b *testing.B, ctx context.Context, svc message.Service, sty *styles.Styles, fetch func() ([]message.Message, error)) {
	var listTotal, nestedTotal, buildTotal, warmTotal time.Duration
	var messages, items int

	for b.Loop() {
		start := time.Now()
		msgs, err := fetch()
		if err != nil {
			b.Fatalf("fetch messages: %v", err)
		}
		listTotal += time.Since(start)
		if len(msgs) == 0 {
			b.Fatal("no messages fetched")
		}

		start = time.Now()
		ptrs := make([]*message.Message, len(msgs))
		for i := range msgs {
			ptrs[i] = &msgs[i]
		}
		toolResults := BuildToolResultMap(ptrs)
		var built []MessageItem
		for _, m := range ptrs {
			built = append(built, ExtractMessageItems(sty, m, toolResults, "", false)...)
		}
		buildTotal += time.Since(start)

		start = time.Now()
		loadNestedBench(ctx, svc, sty, built)
		nestedTotal += time.Since(start)

		start = time.Now()
		listItems := make([]list.Item, len(built))
		for i, it := range built {
			listItems[i] = it
		}
		l := list.NewList(listItems...)
		l.SetSize(100, 40)
		_ = l.TotalHeight()
		warmTotal += time.Since(start)

		messages, items = len(msgs), len(built)
	}

	n := float64(b.N)
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) / n }
	b.ReportMetric(ms(listTotal), "list_ms/op")
	b.ReportMetric(ms(buildTotal), "build_ms/op")
	b.ReportMetric(ms(nestedTotal), "nested_ms/op")
	b.ReportMetric(ms(warmTotal), "warm_ms/op")
	b.Logf("messages=%d items=%d", messages, items)
}

// loadNestedBench mirrors the UI's nested tool call resolution: one more
// transcript read per agent tool call, recursively.
func loadNestedBench(ctx context.Context, svc message.Service, sty *styles.Styles, items []MessageItem) {
	for _, item := range items {
		container, ok := item.(NestedToolContainer)
		if !ok {
			continue
		}
		tool, ok := item.(ToolMessageItem)
		if !ok {
			continue
		}

		childID := fmt.Sprintf("%s$$%s", tool.MessageID(), tool.ToolCall().ID)
		msgs, err := svc.List(ctx, childID)
		if err != nil || len(msgs) == 0 {
			continue
		}

		ptrs := make([]*message.Message, len(msgs))
		for i := range msgs {
			ptrs[i] = &msgs[i]
		}
		results := BuildToolResultMap(ptrs)

		var nested []ToolMessageItem
		for _, m := range ptrs {
			for _, it := range ExtractMessageItems(sty, m, results, "", false) {
				toolItem, ok := it.(ToolMessageItem)
				if !ok {
					continue
				}
				if compact, ok := toolItem.(Compactable); ok {
					compact.SetCompact(true)
				}
				nested = append(nested, toolItem)
			}
		}

		children := make([]MessageItem, len(nested))
		for i, nt := range nested {
			children[i] = nt
		}
		loadNestedBench(ctx, svc, sty, children)

		container.SetNestedTools(nested)
		container.SetTiming(msgs[0].CreatedAt, msgs[len(msgs)-1].CreatedAt)
	}
}
