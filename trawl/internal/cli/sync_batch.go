package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

const syncBatchLockName = "sync.lock"

// runSyncBatch is the one composition path for every sync caller. Acquisition
// is independent, while People reconciliation is deliberately a second,
// ordered phase because every snapshot writes the same People archive.
func (r *Runtime) runSyncBatch(sources []Source, sourceArgs []string, allSources []Source, started func([]Source)) ([]Source, []SyncResult, error) {
	sources = canonicalSyncSources(sources)
	lock, err := acquireSyncBatchLock(r.stateRoot)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = lock.Close() }()
	if started != nil {
		started(sources)
	}

	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	results := runSyncPhases(
		ctx,
		sources,
		func(ctx context.Context, source Source) SyncResult {
			return syncSource(r, ctx, source, sourceArgs)
		},
		func(ctx context.Context, source Source) error {
			return r.reconcileSourcePeopleContext(ctx, source, allSources)
		},
	)
	return sources, results, nil
}

// canonicalSyncSources makes Source.ID the single identity authority for a
// batch. Command aliases may select a source, but they cannot schedule it
// twice. First occurrence defines presentation and result order.
func canonicalSyncSources(sources []Source) []Source {
	canonical := make([]Source, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if _, exists := seen[source.ID]; exists {
			continue
		}
		seen[source.ID] = struct{}{}
		canonical = append(canonical, source)
	}
	return canonical
}

func runSyncPhases(
	ctx context.Context,
	sources []Source,
	acquire func(context.Context, Source) SyncResult,
	reconcile func(context.Context, Source) error,
) []SyncResult {
	results := make([]SyncResult, len(sources))
	var acquisitions sync.WaitGroup
	acquisitions.Add(len(sources))
	for index, source := range sources {
		index, source := index, source
		go func() {
			defer acquisitions.Done()
			results[index] = acquire(ctx, source)
		}()
	}
	acquisitions.Wait()

	for index, source := range sources {
		if syncResultFailed(results[index]) {
			continue
		}
		results[index] = withPeopleSyncFailure(results[index], reconcile(ctx, source))
	}
	return results
}

type syncBatchLock struct {
	file *os.File
}

func acquireSyncBatchLock(stateRoot string) (*syncBatchLock, error) {
	lock, err := acquireArchiveOperationLock(stateRoot, "sync")
	if err != nil {
		var busy archiveOperationBusyError
		if errors.As(err, &busy) {
			return nil, syncAlreadyRunningError(busy)
		}
		return nil, err
	}
	return lock, nil
}

func acquireReplicationLock(stateRoot string) (*syncBatchLock, error) {
	lock, err := acquireArchiveOperationLock(stateRoot, "replicate")
	if err != nil {
		var busy archiveOperationBusyError
		if errors.As(err, &busy) {
			return nil, replicateAlreadyRunningError(busy)
		}
		return nil, err
	}
	return lock, nil
}

// acquireArchiveOperationLock deliberately retains the historical sync.lock
// filename. Older trawl builds already coordinate sync through that inode, so
// changing the path would let an old sync overlap a new replication.
func acquireArchiveOperationLock(stateRoot, operation string) (*syncBatchLock, error) {
	root, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create OpenTrawl state: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(root, syncBatchLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open sync lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK {
			active := readArchiveOperation(file)
			_ = file.Close()
			return nil, archiveOperationBusyError{active: active}
		}
		_ = file.Close()
		return nil, fmt.Errorf("lock sync: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("clear archive operation lock: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("rewind archive operation lock: %w", err)
	}
	if _, err := fmt.Fprintf(file, "operation=%s\npid=%d\nstarted_at=%s\n", operation, os.Getpid(), time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write archive operation lock: %w", err)
	}
	return &syncBatchLock{file: file}, nil
}

func readArchiveOperation(file *os.File) string {
	if file == nil {
		return "sync"
	}
	_, _ = file.Seek(0, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if value, found := strings.CutPrefix(scanner.Text(), "operation="); found {
			value = strings.TrimSpace(value)
			if value == "replicate" || value == "sync" {
				return value
			}
		}
	}
	// A lock held by an older CLI contains no operation metadata. Such a
	// holder can only be a sync, so that is the safe and truthful default.
	return "sync"
}

func (lock *syncBatchLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	_ = syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	return lock.file.Close()
}

type archiveOperationBusyError struct{ active string }

func (e archiveOperationBusyError) Error() string { return "OpenTrawl archive operation is busy" }

type syncAlreadyRunningError struct{ active string }

func (e syncAlreadyRunningError) Error() string {
	if e.active == "replicate" {
		return "OpenTrawl is currently replicating; sync cannot start."
	}
	return "OpenTrawl is already syncing."
}

func (e syncAlreadyRunningError) ErrorBody() ckoutput.ErrorBody {
	return ckoutput.ErrorBody{
		Code:    "already_syncing",
		Message: e.Error(),
	}
}

type replicateAlreadyRunningError struct{ active string }

func (e replicateAlreadyRunningError) Error() string {
	if e.active == "replicate" {
		return "OpenTrawl is already replicating."
	}
	return "OpenTrawl is currently syncing; replication cannot start."
}

func (e replicateAlreadyRunningError) ErrorBody() ckoutput.ErrorBody {
	code := "already_syncing"
	if e.active == "replicate" {
		code = "already_replicating"
	}
	return ckoutput.ErrorBody{Code: code, Message: e.Error()}
}
