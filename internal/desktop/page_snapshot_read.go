package desktop

import (
	"context"

	"silverwing-go/internal/application"
)

type snapshotReader interface {
	Snapshot(context.Context) (application.Snapshot, error)
}

type scopedSnapshotReader interface {
	SnapshotFor(context.Context, application.SnapshotRequest) (application.Snapshot, error)
}

// pageSnapshotRead records which collections were actually read. The UI merges
// against its latest snapshot only when the navigation scope is still current.
type pageSnapshotRead struct {
	snapshot application.Snapshot
	partial  bool
	request  application.SnapshotRequest
}

func readPageSnapshot(ctx context.Context, backend snapshotReader, page, home, config string) (pageSnapshotRead, error) {
	if err := ctx.Err(); err != nil {
		return pageSnapshotRead{}, err
	}
	if scoped, ok := backend.(scopedSnapshotReader); ok && page == "settings" {
		request := application.SnapshotRequest{}
		snapshot, err := scoped.SnapshotFor(ctx, request)
		if err != nil {
			return pageSnapshotRead{}, err
		}
		if err := ctx.Err(); err != nil {
			return pageSnapshotRead{}, err
		}
		// Changed configuration can alter task roots, materials permissions and
		// service identities. Re-read the whole graph rather than combining it
		// with collections from the old configuration.
		if snapshot.Home == home && snapshot.ConfigJSON == config {
			return pageSnapshotRead{snapshot: snapshot, partial: true, request: request}, nil
		}
	}
	snapshot, err := backend.Snapshot(ctx)
	return pageSnapshotRead{snapshot: snapshot}, err
}

func (read pageSnapshotRead) merge(current application.Snapshot) application.Snapshot {
	next := read.snapshot
	if !read.partial {
		return next
	}
	if !read.request.Tasks {
		next.Tasks = current.Tasks
	}
	if !read.request.TagHistory {
		next.TagHistory = current.TagHistory
	}
	if !read.request.Skills {
		next.Skills = current.Skills
	}
	return next
}
