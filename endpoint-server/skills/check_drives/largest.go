package check_drives

import (
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
		// Pick the unexplored directory that currently looks largest.
		sort.Slice(queue, func(i, j int) bool { return queue[i].total > queue[j].total })
		cur := queue[0]
		queue = queue[1:]
		explored++

		entries, rerr := os.ReadDir(cur.path)
		if rerr != nil {
			continue // inaccessible directory, skip it
		}

		for _, e := range entries {
			path := filepath.Join(cur.path, e.Name())
			if e.IsDir() {
				child := &dirNode{path: path, total: dirSelfSize(path), parent: cur}
				candidates = append(candidates, child)
				queue = append(queue, child)
				propagate(cur, child.total)
				continue
			}
			info, ierr := e.Info()
			if ierr != nil || !info.Mode().IsRegular() {
				continue
			}
			fileEntries = append(fileEntries, SizedEntry{Path: path, Size: uint64(info.Size())}) //nolint:gosec
		}
	}

	dirs = topNDirs(candidates, topN)
	files = topNFiles(fileEntries, topN)
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
