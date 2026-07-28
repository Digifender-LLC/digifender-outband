package idrac

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"outband/internal/bmc"
)

func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func truncateSEL(in []bmc.SELEntry, limit int) []bmc.SELEntry {
	if limit > len(in) {
		limit = len(in)
	}
	return append([]bmc.SELEntry(nil), in[:limit]...)
}

func sortSELDesc(entries []bmc.SELEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		ni, _ := strconv.Atoi(entries[i].ID)
		nj, _ := strconv.Atoi(entries[j].ID)
		if ni != 0 || nj != 0 {
			return ni > nj
		}
		if !entries[i].Timestamp.Equal(entries[j].Timestamp) {
			return entries[i].Timestamp.After(entries[j].Timestamp)
		}
		return entries[i].ID > entries[j].ID
	})
}

func parseRedfishTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
