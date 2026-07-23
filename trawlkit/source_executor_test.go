package trawlkit

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func TestSourceExecutorPathsUseCrawlerOverrides(t *testing.T) {
	root := t.TempDir()
	override := filepath.Join(root, "custom", "archive.sqlite")
	source := &pathTestCrawler{
		testCrawler: testCrawler{id: "testcrawl"},
		archive:     override,
	}
	executor := NewSourceExecutor(SourceExecutorOptions{StateRoot: root})

	paths, err := executor.Paths(source)
	if err != nil {
		t.Fatal(err)
	}
	if paths.StateRoot != root || paths.Archive != override {
		t.Fatalf("resolved paths = %#v", paths)
	}
}

type pathTestCrawler struct {
	testCrawler
	archive string
}

func (c *pathTestCrawler) Info() Info {
	info := c.testCrawler.Info()
	info.DefaultPaths.Archive = c.archive
	return info
}

func TestSourceExecutorRejectsSuccessReturnedAfterReadDeadline(t *testing.T) {
	source := &testCrawler{statusFn: func(ctx context.Context, _ *Request) (*control.Status, error) {
		<-ctx.Done()
		status := control.NewStatus("testcrawl", "Test")
		return &status, nil
	}}
	executor := NewSourceExecutor(SourceExecutorOptions{
		StateRoot: t.TempDir(),
		Timeout:   time.Millisecond,
		Stderr:    io.Discard,
	})
	_, err := executor.Status(context.Background(), source)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("status error = %v, want deadline exceeded", err)
	}
}
