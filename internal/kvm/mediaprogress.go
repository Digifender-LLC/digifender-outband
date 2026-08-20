package kvm

import "io"

// MediaActivity reports a background cache download or mount in progress.
type MediaActivity struct {
	Active bool
	Phase  string // downloading | mounting
	Label  string
	URL    string
	Done   int64
	Total  int64 // 0 when unknown
	Err    string
}

func (a MediaActivity) Percent() int {
	if a.Total <= 0 || a.Done <= 0 {
		return 0
	}
	if a.Done >= a.Total {
		return 100
	}
	return int(a.Done * 100 / a.Total)
}

type downloadProgressFunc func(done, total int64)

type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	fn    downloadProgressFunc
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		if p.fn != nil {
			p.fn(p.done, p.total)
		}
	}
	return n, err
}
