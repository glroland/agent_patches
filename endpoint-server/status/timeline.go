package status

import (
	"fmt"
	"time"

	"agent_patches/endpoint-server/memory"
)

// maxTimelineEntries caps the number of timeline entries retained.
const maxTimelineEntries = 50

// AppendTimeline prepends an entry to the "timeline" memory domain, filling
// in ID and Time when unset and trimming to the retention cap. Used by skills
// that record activity outside the report_findings flow (e.g. commands
// executed under a standing approval policy).
func AppendTimeline(mem *memory.Store, e TimelineEntry) error {
	d := mem.Domain("timeline")
	var entries []TimelineEntry
	_ = d.ReadCurrent(&entries)

	if e.ID == "" {
		e.ID = fmt.Sprintf("%s-%d", e.Type, time.Now().UnixNano())
	}
	if e.Time == "" {
		e.Time = time.Now().Format(time.RFC3339)
	}

	entries = append([]TimelineEntry{e}, entries...)
	if len(entries) > maxTimelineEntries {
		entries = entries[:maxTimelineEntries]
	}
	return d.Write(entries)
}
