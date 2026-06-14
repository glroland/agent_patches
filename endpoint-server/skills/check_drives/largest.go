package check_drives

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
)

// SizedEntry represents a filesystem entry (file or directory) and its
// size in bytes.
type SizedEntry struct {
	Path string
	Size uint64
}

// maxDirsExplored bounds how many directories TopLargest will open,
// preventing pathological cases (e.g. huge trees of small files) from
// making the scan unbounded.
const maxDirsExplored = 50

// maxChildrenPerDir bounds how many subdirectories of a single directory are
// queued for further exploration, keeping the top maxChildrenPerDir by their
// own depth-1 size. Without this, a directory with very high fan-out (e.g.
// /etc, with hundreds of small config subdirectories) can flood the queue
// with zero-total entries and exhaust maxDirsExplored before directories
// with deeply nested large files (e.g. /opt) are ever descended into.
const maxChildrenPerDir = 20

// promoteThreshold is the depth-1 size above which a directory is explored
// out of turn (ahead of the breadth-first queue). Without this, a directory
// whose data is buried several levels deep (e.g. /opt, with 0 bytes at
// depth 1) would never be explored before sibling directories that merely
// have a small but non-zero depth-1 size (e.g. hundreds of /etc/*
// subdirectories each containing a few bytes of config) - size-only sorting
// always prefers "tiny but positive" over "zero". Below this threshold,
// directories are explored in breadth-first (discovery) order instead, so
// every depth-1 directory gets a turn before the queue is consumed by
// shallow, low-value candidates.
const promoteThreshold = 50 * 1024 * 1024 // 50 MiB

// pseudoFSDirs lists top-level directory names that host pseudo/virtual
// filesystems on Unix-like systems (procfs, sysfs, devtmpfs, etc.). Their
// reported sizes (e.g. /proc/kcore appearing as the size of physical memory)
// do not reflect real disk usage, so they are excluded from the scan.
var pseudoFSDirs = map[string]bool{
	"proc": true,
	"sys":  true,
	"dev":  true,
	"run":  true,
}

// staticSystemDirs lists top-level directory names that hold the OS
// installation itself (binaries, shared libraries, package data). These
// directories often have a non-trivial depth-1 size and very high fan-out
// (e.g. /usr/share has hundreds of subdirectories), but rarely grow large
// enough to explain a full disk. Their depth-1 size is recorded for ranking,
// but they are not recursed into, freeing the exploration budget for
// directories where application data actually accumulates (e.g. /opt, /var,
// /home).
var staticSystemDirs = map[string]bool{
	"usr":    true,
	"lib":    true,
	"lib32":  true,
	"lib64":  true,
	"libx32": true,
	"bin":    true,
	"sbin":   true,
	"snap":   true,
}

// dirNode tracks a directory discovered during the scan. total starts as
// the directory's own immediate file size (depth 1, no recursion) and grows
// as descendants are discovered and their sizes are propagated upward.
type dirNode struct {
	path   string
	total  uint64
	parent *dirNode
}

// TopLargest finds the top N largest files and top N largest directories
// under root.
//
// It avoids walking the entire tree in one pass: starting at root, it lists
// the immediate children (depth 1). For each subdirectory it computes only
// its own immediate (depth 1) file size, then repeatedly descends into
// whichever unexplored subdirectory currently looks largest, applying the
// same depth-1 technique. Sizes are propagated up to ancestors as
// descendants are discovered, refining the directory totals. The scan stops
// once maxDirsExplored directories have been opened.
func TopLargest(root string, topN int) (dirs []SizedEntry, files []SizedEntry, err error) {
	queue := []*dirNode{{path: root}}
	var candidates []*dirNode
	var fileEntries []SizedEntry
	explored := 0

	for len(queue) > 0 && explored < maxDirsExplored {
		// Pick the next directory to explore: directories that already look
		// substantial (>= promoteThreshold) jump ahead of the queue so large
		// finds get drilled into immediately. Everything else keeps its
		// breadth-first discovery order (stable sort), ensuring every
		// depth-1 directory is explored before the queue is consumed by
		// shallow candidates with merely non-zero totals.
		sort.SliceStable(queue, func(i, j int) bool {
			return queue[i].total >= promoteThreshold && queue[j].total < promoteThreshold
		})
		cur := queue[0]
		queue = queue[1:]
		explored++

		entries, rerr := os.ReadDir(cur.path)
		if rerr != nil {
			slog.Debug("check_drives: skipping inaccessible directory", "path", cur.path, "error", rerr)
			continue // inaccessible directory, skip it
		}
		slog.Debug("check_drives: exploring directory", "path", cur.path, "running_total", cur.total, "explored", explored)

		var children []*dirNode
		for _, e := range entries {
			path := filepath.Join(cur.path, e.Name())
			if e.IsDir() {
				if cur.parent == nil && pseudoFSDirs[e.Name()] {
					slog.Debug("check_drives: skipping pseudo filesystem", "path", path)
					continue
				}
				children = append(children, &dirNode{path: path, total: dirSelfSize(path), parent: cur})
				continue
			}
			info, ierr := e.Info()
			if ierr != nil || !info.Mode().IsRegular() {
				continue
			}
			fileEntries = append(fileEntries, SizedEntry{Path: path, Size: uint64(info.Size())}) //nolint:gosec
		}

		if len(children) > maxChildrenPerDir {
			sort.Slice(children, func(i, j int) bool { return children[i].total > children[j].total })
			slog.Debug("check_drives: capping high fan-out directory", "path", cur.path,
				"subdirectories", len(children), "kept", maxChildrenPerDir)
			children = children[:maxChildrenPerDir]
		}
		for _, child := range children {
			candidates = append(candidates, child)
			propagate(cur, child.total)
			if cur.parent == nil && staticSystemDirs[filepath.Base(child.path)] {
				slog.Debug("check_drives: not recursing into static system directory", "path", child.path)
				continue
			}
			queue = append(queue, child)
		}
	}

	dirs = topNDirs(candidates, topN)
	files = topNFiles(fileEntries, topN)
	slog.Debug("check_drives: scan finished", "root", root, "directories_explored", explored,
		"candidates", len(candidates), "files_seen", len(fileEntries))
	return dirs, files, nil
}

// propagate adds size to node and all of its ancestors' totals.
func propagate(node *dirNode, size uint64) {
	for n := node; n != nil; n = n.parent {
		n.total += size
	}
}

// dirSelfSize returns the total size in bytes of the regular files directly
// inside dir (depth 1, no recursion).
func dirSelfSize(dir string) uint64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total uint64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || !info.Mode().IsRegular() {
			continue
		}
		total += uint64(info.Size()) //nolint:gosec
	}
	return total
}

func topNDirs(candidates []*dirNode, topN int) []SizedEntry {
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].total > candidates[j].total })
	if len(candidates) > topN {
		candidates = candidates[:topN]
	}
	result := make([]SizedEntry, len(candidates))
	for i, c := range candidates {
		result[i] = SizedEntry{Path: c.path, Size: c.total}
	}
	return result
}

func topNFiles(entries []SizedEntry, topN int) []SizedEntry {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Size > entries[j].Size })
	if len(entries) > topN {
		entries = entries[:topN]
	}
	return entries
}
