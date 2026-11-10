package reporting

import (
	"errors"
	"io"
)

// Validate structural limits on bytes before encoding/csv can allocate one
// position per field or accumulate a multiline record. The standard reader
// still decodes fields, reports syntax errors and tracks physical line numbers.
type csvGuard struct {
	r                                 io.Reader
	maxBytes, maxFields, size, commas int
	state                             byte // 0: field start, u: unquoted, q: quoted, c: closing quote, x: malformed tail
	err                               error
}

func (g *csvGuard) Read(p []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	n, err := g.r.Read(p)
	for i, b := range p[:n] {
		g.size++
		if g.state == 'q' {
			if b == '"' {
				g.state = 'c'
			}
		} else if b == '\n' {
			g.size = 0
			g.commas = 0
			g.state = 0
		} else if g.state == 'x' || g.state == 'r' {
			// encoding/csv discards the rest of a malformed physical line.
			g.state = 'x'
		} else if b == ',' {
			g.commas++
			g.state = 0
		} else if b == '"' {
			switch g.state {
			case 0, 'c':
				g.state = 'q'
			default:
				g.state = 'x'
			}
		} else if g.state == 'c' {
			if b == '\r' {
				g.state = 'r'
			} else {
				g.state = 'x'
			}
		} else {
			g.state = 'u'
		}
		if g.size > g.maxBytes || g.commas >= g.maxFields {
			g.err = errors.New("JTL logical record exceeds field or byte limit")
			return i, g.err
		}
	}
	return n, err
}
