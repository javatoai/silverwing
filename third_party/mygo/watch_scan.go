package mygo

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/egoist/mygo/internal/platform"
)

// fileWatchScan keeps what a watch saw of its tree, and which entries the
// backend watches. The system only says where something changed, so the
// watch lists or stats those places again and compares what it finds with
// what it saw: the events are the differences. Only the goroutine of the
// watch uses it.
type fileWatchScan struct {
	w *FileWatcher
	// root is the watched directory, or the directory of the watched file
	// (file), at dir; identity is what it is, and name the file's name.
	root     *os.Root
	identity os.FileInfo
	dir      string
	file     bool
	name     string
	// entries are what the scan saw, by slash-separated path relative to
	// the root ("." for the root), and children the names in each listed
	// directory.
	entries  map[string]os.FileInfo
	children map[string]map[string]bool
	targets  map[string]platform.FileWatchTarget
	ids      map[uint64]string
	next     uint64
	// subtree says the root's target watches the whole tree (Windows), so
	// no other directory is enrolled; noSubtree that the backend can't.
	subtree, noSubtree bool
	// What to look at again in the next update: directories to list,
	// entries to stat, entries the system said were written, and whether
	// to list every directory, after the system lost changes.
	lists, stats, writes map[string]bool
	all                  bool
	// races are entries that changed while the scan looked at them, which
	// are reported as written.
	races map[string]bool
	// ignored holds the directories Ignore ignored, by name in each
	// listed directory, so that notices about what is in them are dropped.
	ignored map[string]map[string]bool
	// before holds what an update changed, as it was before (nil for an
	// entry that didn't exist).
	before map[string]os.FileInfo
}

func newFileWatchScan(w *FileWatcher) *fileWatchScan {
	return &fileWatchScan{
		w:        w,
		entries:  map[string]os.FileInfo{},
		children: map[string]map[string]bool{},
		targets:  map[string]platform.FileWatchTarget{},
		ids:      map[uint64]string{},
		lists:    map[string]bool{},
		stats:    map[string]bool{},
		writes:   map[string]bool{},
		races:    map[string]bool{},
		ignored:  map[string]map[string]bool{},
	}
}

func (s *fileWatchScan) close() {
	if s.root != nil {
		s.root.Close()
	}
}

// start enrolls the root and, below it, what the watch covers. The events
// it returns are for entries that changed while it looked at them.
func (s *fileWatchScan) start() ([]FileEvent, error) {
	info, err := os.Lstat(s.w.root)
	if err != nil {
		return nil, err
	}
	info = StatIdentity(info)
	s.dir = s.w.root
	if !info.IsDir() {
		// A file is watched through its directory, which names it.
		s.file = true
		s.dir, s.name = filepath.Dir(s.w.root), filepath.Base(s.w.root)
	}
	if s.root, err = fileWatchOpenRoot(s.dir); err != nil {
		return nil, err
	}
	// The identity through the handle, as Windows loads that of a path
	// only when it is first compared.
	if s.identity, err = s.root.Stat("."); err != nil {
		return nil, err
	}
	s.identity = StatIdentity(s.identity)
	if s.file {
		if info, err = os.Lstat(s.dir); err != nil {
			return nil, err
		}
		info = StatIdentity(info)
	}
	if !os.SameFile(info, s.identity) {
		return nil, &os.PathError{Op: "watch", Path: s.dir, Err: errWatchRootChanged}
	}
	if err = s.watchParent(); err != nil {
		return nil, err
	}
	info = s.identity
	if s.file {
		if info, err = s.root.Lstat(s.name); err != nil {
			return nil, err
		}
	}
	s.before = map[string]os.FileInfo{}
	if err = s.visit(".", info); err != nil {
		return nil, err
	}
	// The initial contents are not events.
	clear(s.before)
	return s.events(), nil
}

// watchParent enrolls the directory of a watched file, and the parent of
// the watched directory on Windows, where the directory's own handle
// reports nothing when it is renamed or replaced; "..", whose notices
// about other entries are ignored. A drive's root, which can't be renamed,
// has no parent.
func (s *fileWatchScan) watchParent() error {
	if !s.file && runtime.GOOS != "windows" {
		return nil
	}
	parent := filepath.Dir(s.w.root)
	if parent == s.w.root {
		return nil
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	return s.add("..", StatIdentity(info))
}

// gone reports whether the watched directory, or the directory of the
// watched file, went away or was replaced.
func (s *fileWatchScan) gone() bool {
	info, err := os.Lstat(s.dir)
	return errors.Is(err, fs.ErrNotExist) || err == nil && (!info.IsDir() || !os.SameFile(StatIdentity(info), s.identity))
}

// goneEvents are the last events of a watch whose directory went away.
func (s *fileWatchScan) goneEvents() []FileEvent {
	info := s.entries["."]
	if info == nil {
		return nil // a watched file that was already gone
	}
	return []FileEvent{{Op: FileRemove, Path: ".", IsDir: info.IsDir()}}
}

// osPath is the path of p for root.
func (s *fileWatchScan) osPath(p string) string {
	if s.file {
		return s.name
	}
	return filepath.FromSlash(p)
}

// ignores reports whether p is in a directory Ignore ignored.
func (s *fileWatchScan) ignores(p string) bool {
	for p != "." {
		if s.ignored[path.Dir(p)][path.Base(p)] {
			return true
		}
		p = path.Dir(p)
	}
	return false
}

// skip reports whether Ignore ignores child, and remembers in ignored the
// directories it does.
func (s *fileWatchScan) skip(child string, isDir bool, ignored map[string]bool) bool {
	if s.w.opts.Ignore == nil || !s.w.opts.Ignore(child, isDir) {
		return false
	}
	if isDir {
		ignored[path.Base(child)] = true
	}
	return true
}

// mark records what n says changed, for the next update. It returns an
// error when n ends the watch.
func (s *fileWatchScan) mark(n platform.FileWatchNotice) error {
	if n.Err != nil {
		if errors.Is(n.Err, platform.ErrFileWatchOverflow) {
			s.all = true
			return nil
		}
		if _, ok := s.ids[n.Target]; !ok && n.Target != 0 {
			return nil // a target removed since
		}
		return n.Err
	}
	p, ok := s.ids[n.Target]
	if !ok {
		return nil
	}
	if p == ".." {
		name := filepath.Base(s.w.root)
		if n.Kind == platform.FileWatchLost {
			// The next update enrolls it again, or ends the watch.
			s.unenroll("..")
		} else if n.Name != name && (n.Name != "" || !s.file) {
			return nil
		}
		if s.file {
			s.stats["."] = true
			if n.Kind == platform.FileWatchWrite {
				s.writes["."] = true
			}
		} else {
			s.lists["."] = true
		}
		return nil
	}
	if s.file {
		// The file itself (macOS).
		if n.Kind == platform.FileWatchLost {
			s.unenroll(".")
		}
		s.stats["."] = true
		if n.Kind == platform.FileWatchWrite {
			s.writes["."] = true
		}
		return nil
	}
	t := s.targets[p]
	listable := t.Directory && (p == "." || s.w.opts.Recursive)
	if n.Kind == platform.FileWatchLost {
		// The next update enrolls it again if it still exists, and looks
		// at everything if it watched the whole tree.
		if p == "." && s.subtree {
			s.all = true
		}
		s.unenroll(p)
		n = platform.FileWatchNotice{Kind: platform.FileWatchDirty}
	}
	if n.Name == "" || !listable {
		if listable {
			s.lists[p] = true
		} else {
			s.stats[p] = true
		}
		if n.Kind == platform.FileWatchWrite {
			s.writes[p] = true
		}
		return nil
	}
	if !validWatchName(n.Name, t.Subtree) {
		return errors.New("mygo: invalid name in a file watch notice")
	}
	// Statting a child lists its directory if it appeared, went away or
	// was replaced.
	child := path.Join(p, n.Name)
	if s.ignores(child) {
		return nil
	}
	s.stats[child] = true
	if n.Kind == platform.FileWatchWrite {
		s.writes[child] = true
	}
	if len(s.lists)+len(s.stats)+len(s.writes) > s.w.opts.MaxEntries {
		s.all = true
		clear(s.lists)
		clear(s.stats)
		clear(s.writes)
	}
	return nil
}

// validWatchName reports whether name names a child, or for a subtree a
// path beneath it.
func validWatchName(name string, subtree bool) bool {
	if strings.ContainsRune(name, 0) || strings.ContainsRune(name, filepath.Separator) && filepath.Separator != '/' {
		return false
	}
	if !subtree && strings.Contains(name, "/") {
		return false
	}
	for part := range strings.SplitSeq(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// pending reports whether an update has anything to do.
func (s *fileWatchScan) pending() bool {
	return s.all || len(s.lists) > 0 || len(s.stats) > 0 || len(s.writes) > 0
}

// update looks again at what changed and returns the events.
func (s *fileWatchScan) update() ([]FileEvent, error) {
	if err := s.w.ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(s.dir)
	if err != nil {
		return nil, err
	}
	if info = StatIdentity(info); !info.IsDir() || !os.SameFile(info, s.identity) {
		return nil, &os.PathError{Op: "watch", Path: s.dir, Err: errWatchRootChanged}
	}
	if err = s.watchParent(); err != nil {
		return nil, err
	}
	s.before = map[string]os.FileInfo{}
	if s.file {
		all := s.all
		s.all = false
		if all || len(s.stats) > 0 {
			if err := s.statFile(); err != nil {
				return nil, err
			}
		}
		clear(s.stats)
		return s.events(), nil
	}
	if s.all {
		s.all = false
		for p, info := range s.entries {
			if info.IsDir() && (p == "." || s.w.opts.Recursive) {
				s.lists[p] = true
			}
		}
	}
	for p := range s.stats {
		if err := s.stat(p); err != nil {
			return nil, err
		}
	}
	// Parents first: listing a directory again can replace those below it.
	dirs := make([]string, 0, len(s.lists))
	for p := range s.lists {
		dirs = append(dirs, p)
	}
	sort.Slice(dirs, func(i, j int) bool {
		if a, b := strings.Count(dirs[i], "/"), strings.Count(dirs[j], "/"); a != b {
			return a < b
		}
		return dirs[i] < dirs[j]
	})
	for _, p := range dirs {
		if s.entries[p] == nil {
			continue // removed with its parent
		}
		if err := s.list(p); err != nil {
			return nil, err
		}
	}
	clear(s.lists)
	clear(s.stats)
	return s.events(), nil
}

// events compares what the last update changed with what it was, then
// forgets both.
func (s *fileWatchScan) events() []FileEvent {
	writes := s.writes
	for p := range s.races {
		writes[p] = true
	}
	for p := range writes {
		if s.entries[p] != nil {
			s.touch(p)
		}
	}
	before := make(map[string]os.FileInfo, len(s.before))
	after := make(map[string]os.FileInfo, len(s.before))
	for p, info := range s.before {
		if info != nil {
			before[p] = info
		}
		if info := s.entries[p]; info != nil {
			after[p] = info
		}
	}
	events := fileWatchDiff(before, after, writes)
	if a, b := before["."], after["."]; s.file && a != nil && b != nil && !a.IsDir() && !b.IsDir() && !os.SameFile(a, b) {
		// A file replaced, as editors save, is written.
		events = []FileEvent{{Op: FileWrite, Path: "."}}
	}
	clear(s.before)
	clear(s.writes)
	clear(s.races)
	return events
}

// statFile looks at the watched file again.
func (s *fileWatchScan) statFile() error {
	old := s.entries["."]
	info, err := s.root.Lstat(s.name)
	if errors.Is(err, fs.ErrNotExist) {
		if old != nil {
			s.removeTree(".")
		}
		return nil
	}
	if s.inaccessible(".", err) {
		return nil
	}
	if err != nil {
		return err
	}
	if old != nil && os.SameFile(old, info) {
		s.set(".", info)
		return s.enroll(".", info)
	}
	if old != nil {
		s.removeTree(".")
	}
	return s.visit(".", info)
}

// stat looks at p again. For an entry it didn't know, or one that is gone
// or was replaced, it lists the directory that has it instead.
func (s *fileWatchScan) stat(p string) error {
	known := p
	for known != "." && s.entries[known] == nil {
		known = path.Dir(known)
	}
	if known != p && path.Dir(p) == known && s.w.opts.Ignore != nil && s.listable(known) {
		// Something new that Ignore may ignore, which needs no listing.
		info, err := s.root.Lstat(filepath.FromSlash(p))
		if err == nil && s.skip(p, info.IsDir(), s.ignoredIn(known)) {
			return nil
		}
	}
	if known == "." || known != p && s.listable(known) {
		s.lists[known] = true
		return nil
	}
	p = known
	old := s.entries[p]
	info, err := s.root.Lstat(filepath.FromSlash(p))
	if err == nil && os.SameFile(old, info) {
		s.set(p, info)
		return s.enroll(p, info)
	}
	if s.inaccessible(p, err) {
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.lists[path.Dir(p)] = true
	return nil
}

// inaccessible reports whether err says p can't be looked at, as a
// directory without permission or, on Windows, a file the system holds
// (pagefile.sys): it is left as it was seen, if at all, rather than
// ending the watch. The watched directory itself can't be.
func (s *fileWatchScan) inaccessible(p string, err error) bool {
	return err != nil && (p != "." || s.file) && fileWatchInaccessible(err)
}

// ignoredIn returns the directories ignored in directory p.
func (s *fileWatchScan) ignoredIn(p string) map[string]bool {
	if s.ignored[p] == nil {
		s.ignored[p] = map[string]bool{}
	}
	return s.ignored[p]
}

// listable reports whether p is a directory the watch lists.
func (s *fileWatchScan) listable(p string) bool {
	info := s.entries[p]
	return info != nil && s.descends(p, info)
}

// list lists directory p again: what appeared or was replaced in it is
// visited, what is gone removed, and the rest stat'ed again.
func (s *fileWatchScan) list(p string) error {
	dir, info, err := s.openDir(p, s.entries[p])
	if err != nil {
		if p == "." {
			return err
		}
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errWatchRootChanged) {
			// Gone or replaced: what has it knows.
			return s.list(path.Dir(p))
		}
		if s.inaccessible(p, err) {
			return nil
		}
		return err
	}
	defer dir.Close()
	if err := s.enroll(p, info); err != nil {
		return err
	}
	s.set(p, info)
	seen := map[string]bool{}
	ignored := map[string]bool{}
	s.ignored[p] = ignored
	for {
		entries, err := dir.ReadDir(128)
		for _, entry := range entries {
			name := entry.Name()
			child := path.Join(p, name)
			if s.skip(child, entry.IsDir(), ignored) {
				continue
			}
			seen[name] = true
			info, err := s.root.Lstat(filepath.FromSlash(child))
			if errors.Is(err, fs.ErrNotExist) {
				s.races[p] = true
				delete(seen, name)
				continue
			}
			if s.inaccessible(child, err) {
				continue // as it was, if it was known
			}
			if err != nil {
				return err
			}
			if old := s.entries[child]; old != nil {
				if os.SameFile(old, info) {
					s.set(child, info)
					if err := s.enroll(child, info); err != nil {
						return err
					}
					continue
				}
				s.removeTree(child)
			}
			if err := s.visit(child, info); err != nil {
				return err
			}
		}
		if err == io.EOF {
			break
		}
		if s.inaccessible(p, err) {
			return nil // what is gone is not known
		}
		if err != nil {
			return err
		}
	}
	for name := range s.children[p] {
		if !seen[name] {
			s.removeTree(path.Join(p, name))
		}
	}
	return nil
}

// openDir opens directory p, which must still be the directory info.
func (s *fileWatchScan) openDir(p string, info os.FileInfo) (*os.File, os.FileInfo, error) {
	dir, err := s.root.Open(filepath.FromSlash(p))
	if err != nil {
		return nil, nil, err
	}
	opened, err := dir.Stat()
	if err != nil {
		dir.Close()
		return nil, nil, err
	}
	if !opened.IsDir() || !os.SameFile(info, opened) {
		dir.Close()
		return nil, nil, &os.PathError{Op: "watch", Path: p, Err: errWatchRootChanged}
	}
	return dir, opened, nil
}

// visit records p, which wasn't known, and when the watch covers it what
// is beneath it. An entry removed or replaced while it looks is skipped,
// and its directory reported as written.
func (s *fileWatchScan) visit(p string, info os.FileInfo) error {
	if err := s.w.ctx.Err(); err != nil {
		return err
	}
	if len(s.entries) >= s.w.opts.MaxEntries || len(s.races) > s.w.opts.MaxEntries {
		return ErrWatchLimit
	}
	gone := func() error {
		s.unenroll(p)
		s.races[path.Dir(p)] = true
		return nil
	}
	// Enroll first, then check the entry is still what was enrolled, so
	// that nothing changes unseen in between. Replacements, as editors'
	// atomic saves do, are retried a few times.
	var dir *os.File
	for attempt := 0; ; attempt++ {
		if attempt == 3 {
			return gone()
		}
		err := s.enroll(p, info)
		if errors.Is(err, fs.ErrPermission) || errors.Is(err, platform.ErrUnsupported) || errors.Is(err, context.Canceled) {
			return err
		}
		checked, statErr := s.root.Lstat(s.osPath(p))
		if (p != "." || s.file) && (errors.Is(statErr, fs.ErrNotExist) || errors.Is(err, fs.ErrNotExist)) {
			return gone()
		}
		if statErr != nil {
			return statErr
		}
		if !os.SameFile(info, checked) {
			if p == "." && !s.file {
				return errWatchRootChanged
			}
			s.races[p] = true
			info = checked
			continue
		}
		if err != nil {
			return err
		}
		info = checked
		if !s.descends(p, info) {
			break
		}
		opened, openedInfo, err := s.openDir(p, info)
		if err == nil {
			dir, info = opened, openedInfo
			break
		}
		if s.inaccessible(p, err) {
			break // known, but not what is in it
		}
		if p == "." {
			return err
		}
		if errors.Is(err, fs.ErrNotExist) {
			return gone()
		}
		if !errors.Is(err, errWatchRootChanged) {
			return err
		}
		s.races[p] = true
		if info, err = s.root.Lstat(s.osPath(p)); errors.Is(err, fs.ErrNotExist) {
			return gone()
		} else if err != nil {
			return err
		}
	}
	s.set(p, info)
	if dir == nil {
		return nil
	}
	defer dir.Close()
	ignored := s.ignoredIn(p)
	for {
		entries, err := dir.ReadDir(128)
		for _, entry := range entries {
			child := path.Join(p, entry.Name())
			if s.skip(child, entry.IsDir(), ignored) {
				continue
			}
			info, err := s.root.Lstat(filepath.FromSlash(child))
			if errors.Is(err, fs.ErrNotExist) {
				s.races[p] = true
				continue
			}
			if s.inaccessible(child, err) {
				continue
			}
			if err != nil {
				return err
			}
			if err := s.visit(child, info); err != nil {
				return err
			}
		}
		if err == io.EOF || s.inaccessible(p, err) {
			return nil // what it could list
		}
		if err != nil {
			return err
		}
	}
}

// descends reports whether the watch covers what is in p.
func (s *fileWatchScan) descends(p string, info os.FileInfo) bool {
	// Symlinks and Windows junctions are not directories to Lstat.
	return info.IsDir() && !s.file && (p == "." || s.w.opts.Recursive)
}

// touch remembers p as it is before an update changes it.
func (s *fileWatchScan) touch(p string) {
	if _, ok := s.before[p]; !ok {
		s.before[p] = s.entries[p]
	}
}

func (s *fileWatchScan) set(p string, info os.FileInfo) {
	s.touch(p)
	// What an update compares with what came before it, so the identity
	// doesn't follow a later replacement of the entry (StatIdentity).
	s.entries[p] = StatIdentity(info)
	if p != "." {
		dir := path.Dir(p)
		if s.children[dir] == nil {
			s.children[dir] = map[string]bool{}
		}
		s.children[dir][path.Base(p)] = true
	}
}

// removeTree forgets p and what is beneath it.
func (s *fileWatchScan) removeTree(p string) {
	for name := range s.children[p] {
		s.removeTree(path.Join(p, name))
	}
	delete(s.children, p)
	delete(s.ignored, p)
	s.touch(p)
	delete(s.entries, p)
	if p != "." {
		delete(s.children[path.Dir(p)], path.Base(p))
	}
	s.unenroll(p)
}

// enroll has the backend watch p, which the scan saw as info, unless the
// root's target covers it or it is a symlink or a special file. A watched
// file is enrolled only while it is a file.
func (s *fileWatchScan) enroll(p string, info os.FileInfo) error {
	if !info.Mode().IsRegular() && (!info.IsDir() || s.file) || s.subtree && p != "." {
		s.unenroll(p)
		return nil
	}
	if err := s.add(p, info); !s.inaccessible(p, err) {
		return err
	}
	return nil // seen, but not watched
}

func (s *fileWatchScan) add(p string, info os.FileInfo) error {
	if old, ok := s.targets[p]; ok {
		if os.SameFile(old.Info, info) {
			return nil
		}
		s.unenroll(p)
	}
	s.next++
	t := platform.FileWatchTarget{ID: s.next, Path: filepath.Join(s.w.root, filepath.FromSlash(p)), Directory: info.IsDir(), Info: info}
	if p == "." && s.w.opts.Recursive && !s.noSubtree {
		t.Subtree = true
		err := s.w.native.Add(t)
		if errors.Is(err, platform.ErrUnsupported) {
			s.noSubtree = true
			t.Subtree = false
		} else if err != nil {
			return &os.PathError{Op: "watch", Path: t.Path, Err: err}
		} else {
			// Directories below enrolled while the root had no target are
			// covered by it now.
			for q := range s.targets {
				if q != "." && q != ".." {
					s.unenroll(q)
				}
			}
			s.subtree = true
			s.record(p, t)
			return nil
		}
	}
	if err := s.w.native.Add(t); err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return &os.PathError{Op: "watch", Path: t.Path, Err: err}
	}
	s.record(p, t)
	return nil
}

func (s *fileWatchScan) record(p string, t platform.FileWatchTarget) {
	s.targets[p] = t
	s.ids[t.ID] = p
}

func (s *fileWatchScan) unenroll(p string) {
	if t, ok := s.targets[p]; ok {
		delete(s.targets, p)
		delete(s.ids, t.ID)
		s.w.native.Remove(t.ID)
		if p == "." {
			s.subtree = false
		}
	}
}

// fileWatchDiff turns what an update changed, as it was before and is
// after, into events. An entry whose identity moved to another path is a
// rename, when nothing else has that identity.
func fileWatchDiff(before, after map[string]os.FileInfo, writes map[string]bool) []FileEvent {
	removed := make(map[string]os.FileInfo)
	created := make(map[string]os.FileInfo)
	for p, a := range before {
		if b, ok := after[p]; !ok || !os.SameFile(a, b) {
			removed[p] = a
		}
	}
	for p, b := range after {
		if a, ok := before[p]; !ok || !os.SameFile(a, b) {
			created[p] = b
		}
	}
	type group struct {
		path   string
		events []FileEvent
	}
	var groups []group
	paired := make(map[string]string)
	keys := make([]string, 0, len(removed))
	for p := range removed {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	unique := func(m map[string]os.FileInfo, info os.FileInfo) bool {
		count := 0
		for _, v := range m {
			if os.SameFile(info, v) {
				if count++; count > 1 {
					return false
				}
			}
		}
		return count == 1
	}
	for _, old := range keys {
		a := removed[old]
		match := ""
		count := 0
		for p, b := range created {
			if os.SameFile(a, b) {
				match = p
				count++
			}
		}
		// A hard link makes pairing by identity ambiguous.
		if count != 1 || !unique(before, a) || !unique(after, a) {
			continue
		}
		// A replacement is a removal and a creation even if the old name
		// survives elsewhere.
		if _, exists := after[old]; exists {
			continue
		}
		if _, exists := before[match]; exists {
			continue
		}
		// Moved with a directory renamed above it, which says it all.
		descendant := false
		for parent := path.Dir(old); parent != "."; parent = path.Dir(parent) {
			if dest, ok := paired[parent]; ok && match == dest+strings.TrimPrefix(old, parent) {
				descendant = true
				break
			}
		}
		paired[old] = match
		delete(removed, old)
		b := created[match]
		delete(created, match)
		var events []FileEvent
		if !descendant {
			events = append(events, FileEvent{Op: FileRename, OldPath: old, Path: match, IsDir: a.IsDir()})
		}
		if !b.IsDir() && (writes[old] || writes[match] || fileInfoChanged(a, b)) {
			events = append(events, FileEvent{Op: FileWrite, Path: match})
		}
		groups = append(groups, group{match, events})
	}
	paths := make(map[string]bool)
	for p := range before {
		paths[p] = true
	}
	for p := range after {
		paths[p] = true
	}
	for p := range paths {
		var events []FileEvent
		if a, ok := removed[p]; ok {
			events = append(events, FileEvent{Op: FileRemove, Path: p, IsDir: a.IsDir()})
		}
		if b, ok := created[p]; ok {
			events = append(events, FileEvent{Op: FileCreate, Path: p, IsDir: b.IsDir()})
		}
		a, aok := before[p]
		b, bok := after[p]
		if aok && bok && !b.IsDir() && os.SameFile(a, b) && (writes[p] || fileInfoChanged(a, b)) {
			events = append(events, FileEvent{Op: FileWrite, Path: p})
		}
		if len(events) > 0 {
			groups = append(groups, group{p, events})
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].path < groups[j].path })
	var result []FileEvent
	for _, g := range groups {
		result = append(result, g.events...)
	}
	return result
}

// fileInfoChanged reports whether a file's content may have changed, as
// its size or modification time says; its permissions and other metadata,
// which backup tools, indexers and antivirus touch, don't count.
func fileInfoChanged(a, b os.FileInfo) bool {
	return a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime())
}
