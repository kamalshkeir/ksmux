// Copyright 2013 Julien Schmidt. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package ksmux

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kamalshkeir/lg"
)

type typeOfNode uint8

const (
	static typeOfNode = iota
	root
	param
	catchAll
)

const defaultParamsSize = 20

type paramInfo struct {
	position int
	names    []string
}

type node struct {
	path      string
	handler   Handler
	children  []*node
	nType     typeOfNode
	paramInfo *paramInfo
	indexes   string
	isWild    bool
	origines  string
}

// addPath adds a node with the given handler to the path.
// Not concurrency-safe!
func (n *node) addPath(path string, handler Handler, origines ...string) {
	lg.Debug("addPath called", "path", path)

	if n.path == "" && n.indexes == "" {
		lg.Debug("empty tree, inserting root")
		n.insertChild(path, handler, origines...)
		n.nType = root
		return
	}

walk:
	for {
		i := longestCommonPrefix(path, n.path)
		lg.Debug("found common prefix", "len", i, "path", path, "nodePath", n.path)

		if i < len(n.path) {
			lg.Debug("splitting edge", "path", path, "nodePath", n.path)
			child := node{
				path:     n.path[i:],
				nType:    static,
				indexes:  n.indexes,
				children: n.children,
				handler:  n.handler,
				origines: n.origines,
			}
			n.children = []*node{&child}
			n.indexes = string([]byte{n.path[i]})
			n.path = path[:i]
			n.handler = nil
		}

		if i < len(path) {
			path = path[i:]
			lg.Debug("remaining path", "path", path)

			// Check for parameter
			if path[0] == ':' {
				lg.Debug("found parameter in path", "path", path)
				for i, index := range []byte(n.indexes) {
					if index == ':' {
						lg.Debug("found existing param node", "index", i)
						n = n.children[i]
						continue walk
					}
				}
			}

			// Find matching child
			for i, index := range []byte(n.indexes) {
				if path[0] == index {
					lg.Debug("found matching child", "index", i, "char", string(index))
					n = n.children[i]
					continue walk
				}
			}

			// No matching child
			lg.Debug("no matching child, creating new one", "path", path)
			n.indexes += string([]byte{path[0]})
			child := &node{}
			n.children = append(n.children, child)
			n = child
			n.insertChild(path, handler, origines...)
			return
		}

		n.handler = handler
		n.origines = strings.Join(origines, ",")
		return
	}
}

func (n *node) insertChild(path string, handler Handler, origines ...string) {
	if path == "/" {
		n.handler = handler
		if len(origines) > 0 {
			// Pre-allocate string builder for origines
			var sb strings.Builder
			sb.Grow(len(origines) * 8) // Estimate 8 chars per origin
			for i, origin := range origines {
				if i > 0 {
					sb.WriteByte(',')
				}
				sb.WriteString(origin)
			}
			n.origines = sb.String()
		}
		return
	}

	segments := strings.Split(strings.Trim(path, "/"), "/")
	current := n
	paramCount := 0

	for _, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			paramCount++
			paramName := segment[1:]

			// Look for existing param node at this position
			var paramNode *node
			for _, child := range current.children {
				if child.nType == param && child.paramInfo.position == paramCount {
					paramNode = child
					found := false
					for _, name := range paramNode.paramInfo.names {
						if name == paramName {
							found = true
							break
						}
					}
					if !found {
						paramNode.paramInfo.names = append(paramNode.paramInfo.names, paramName)
					}
					break
				}
			}

			if paramNode == nil {
				paramNode = &node{
					nType:  param,
					path:   ":" + strconv.Itoa(paramCount),
					isWild: true,
					paramInfo: &paramInfo{
						position: paramCount,
						names:    []string{paramName},
					},
				}
				current.children = append(current.children, paramNode)
			}

			current = paramNode
			continue
		}

		if strings.HasPrefix(segment, "*") {
			child := &node{
				nType:    catchAll,
				path:     segment,
				handler:  handler,
				origines: strings.Join(origines, ","),
				isWild:   true,
			}
			current.children = []*node{child}
			return
		}

		// Static path
		var staticNode *node
		for _, child := range current.children {
			if child.nType == static && child.path == segment {
				staticNode = child
				break
			}
		}

		if staticNode == nil {
			staticNode = &node{
				nType: static,
				path:  segment,
			}
			current.children = append(current.children, staticNode)
		}

		current = staticNode
	}

	current.handler = handler
	current.origines = strings.Join(origines, ",")
}

// Returns the handler registered with the given path (key). The values of
// wildcards are saved to a map.
// If no handler can be found, a TSR (trailing slash redirect) recommendation is
// made if a handler exists with an extra (without the) trailing slash for the
// given path.
func (n *node) getHandler(path string, params func() *Params) (handler Handler, ps *Params, tsr bool, origines string) {
	if path == "/" {
		return n.handler, nil, false, n.origines
	}

	pathLen := len(path)
	if pathLen > 0 && path[0] == '/' {
		path = path[1:]
		pathLen--
	}

	// Pre-calculate param count to avoid slice reallocation
	paramCount := countParams(path)
	if params != nil && paramCount > 0 {
		ps = params()
		size := int(defaultParamsSize)
		if paramCount > uint16(defaultParamsSize) {
			size = int(paramCount)
		}
		*ps = make([]Param, 0, size)
	}

	current := n
	start := 0
	paramPos := 0
	pathBytes := []byte(path) // Avoid repeated string indexing

	for i := 0; i < pathLen; i++ {
		if pathBytes[i] == '/' || i == pathLen-1 {
			end := i
			if i == pathLen-1 {
				end = pathLen
			}
			segment := path[start:end] // Direct slice, no buffer needed

			// Try static nodes first
			var next *node
			for _, child := range current.children {
				if child.nType == static && child.path == segment {
					next = child
					break
				}
			}

			// Try parameter nodes
			if next == nil {
				paramPos++
				for _, child := range current.children {
					if child.nType == param && child.paramInfo.position == paramPos {
						if params != nil {
							if ps == nil {
								ps = params()
							}
							names := child.paramInfo.names
							l := len(names)
							for j := 0; j < l; j++ {
								*ps = append(*ps, Param{
									Key:   names[j],
									Value: segment,
								})
							}
						}
						next = child
						break
					}
				}
			}

			// Try catchAll
			if next == nil {
				for _, child := range current.children {
					if child.nType == catchAll {
						remaining := path[start:]
						if params != nil {
							if ps == nil {
								ps = params()
							}
							paramName := child.path[1:] // Avoid TrimPrefix allocation
							*ps = append(*ps, Param{
								Key:   paramName,
								Value: remaining,
							})
						}
						return child.handler, ps, false, child.origines
					}
				}
			}

			if next == nil {
				return nil, nil, false, ""
			}

			current = next
			start = i + 1
		}
	}

	return current.handler, ps, false, current.origines
}

func (n *node) getCaseInsensitivePath(path string, fixTrailingSlash bool) (fixedPath string, found bool) {
	const stackBufSize = 128
	buf := make([]byte, 0, stackBufSize)
	if l := len(path) + 1; l > stackBufSize {
		buf = make([]byte, 0, l)
	}

	ciPath := n.getCaseInsensitivePathRec(
		path,
		buf,
		[4]byte{},
		fixTrailingSlash,
	)

	return string(ciPath), ciPath != nil
}

// Shift bytes in array by n bytes left
func shiftNRuneBytes(rb [4]byte, n int) [4]byte {
	switch n {
	case 0:
		return rb
	case 1:
		return [4]byte{rb[1], rb[2], rb[3], 0}
	case 2:
		return [4]byte{rb[2], rb[3]}
	case 3:
		return [4]byte{rb[3]}
	default:
		return [4]byte{}
	}
}

// getCaseInsensitivePathRec get a recursive case-insensitive lookup function used by n.findCaseInsensitivePath
func (n *node) getCaseInsensitivePathRec(path string, ciPath []byte, rb [4]byte, fixTrailingSlash bool) []byte {
	npLen := len(n.path)

walk: // Outer loop for walking the tree
	for len(path) >= npLen && (npLen == 0 || strings.EqualFold(path[1:npLen], n.path[1:])) {
		// Add common prefix to result
		oldPath := path
		path = path[npLen:]
		ciPath = append(ciPath, n.path...)

		if len(path) > 0 {
			// If this node does not have a wildcard (param or catchAll) child,
			// we can just look up the next child node and continue to walk down
			// the tree
			if !n.isWild {
				// Skip rune bytes already processed
				rb = shiftNRuneBytes(rb, npLen)

				if rb[0] != 0 {
					// Old rune not finished
					idxc := rb[0]
					for i, c := range []byte(n.indexes) {
						if c == idxc {
							// continue with child node
							n = n.children[i]
							npLen = len(n.path)
							continue walk
						}
					}
				} else {
					// Process a new rune
					var rv rune

					// Find rune start.
					// Runes are up to 4 byte long,
					// -4 would definitely be another rune.
					var off int
					for max := min(npLen, 3); off < max; off++ {
						if i := npLen - off; utf8.RuneStart(oldPath[i]) {
							// read rune from cached path
							rv, _ = utf8.DecodeRuneInString(oldPath[i:])
							break
						}
					}

					// Calculate lowercase bytes of current rune
					lo := unicode.ToLower(rv)
					utf8.EncodeRune(rb[:], lo)

					// Skip already processed bytes
					rb = shiftNRuneBytes(rb, off)

					idxc := rb[0]
					for i, c := range []byte(n.indexes) {
						// Lowercase matches
						if c == idxc {
							// must use a recursive approach since both the
							// uppercase byte and the lowercase byte might exist
							// as an index
							if out := n.children[i].getCaseInsensitivePathRec(
								path, ciPath, rb, fixTrailingSlash,
							); out != nil {
								return out
							}
							break
						}
					}

					// If we found no match, the same for the uppercase rune,
					// if it differs
					if up := unicode.ToUpper(rv); up != lo {
						utf8.EncodeRune(rb[:], up)
						rb = shiftNRuneBytes(rb, off)

						idxc := rb[0]
						for i, c := range []byte(n.indexes) {
							// Uppercase matches
							if c == idxc {
								// Continue with child node
								n = n.children[i]
								npLen = len(n.path)
								continue walk
							}
						}
					}
				}

				// Nothing found. We can recommend to redirect to the same URL
				// without a trailing slash if a leaf exists for that path
				if fixTrailingSlash && path == "/" && n.handler != nil {
					return ciPath
				}
				return nil
			}

			n = n.children[0]
			switch n.nType {
			case param:
				// Find param end (either '/' or path end)
				end := 0
				for end < len(path) && path[end] != '/' {
					end++
				}

				// Add param value to case insensitive path
				ciPath = append(ciPath, path[:end]...)

				// We need to go deeper!
				if end < len(path) {
					if len(n.children) > 0 {
						// Continue with child node
						n = n.children[0]
						npLen = len(n.path)
						path = path[end:]
						continue
					}

					// ... but we can't
					if fixTrailingSlash && len(path) == end+1 {
						return ciPath
					}
					return nil
				}

				if n.handler != nil {
					return ciPath
				} else if fixTrailingSlash && len(n.children) == 1 {
					// No handler found. Check if a handler for this path + a
					// trailing slash exists
					n = n.children[0]
					if n.path == "/" && n.handler != nil {
						return append(ciPath, '/')
					}
				}
				return nil

			case catchAll:
				return append(ciPath, path...)

			default:
				lg.Fatal(fmt.Sprintf("invalid node type '%v'", n.nType))
			}
		} else {
			// We should have reached the node containing the handler.
			// Check if this node has a handler registered.
			if n.handler != nil {
				return ciPath
			}

			// No handler found.
			// Try to fix the path by adding a trailing slash
			if fixTrailingSlash {
				for i, c := range []byte(n.indexes) {
					if c == '/' {
						n = n.children[i]
						if (len(n.path) == 1 && n.handler != nil) ||
							(n.nType == catchAll && n.children[0].handler != nil) {
							return append(ciPath, '/')
						}
						return nil
					}
				}
			}
			return nil
		}
	}

	// Nothing found.
	// Try to fix the path by adding / removing a trailing slash
	if fixTrailingSlash {
		if path == "/" {
			return ciPath
		}
		if len(path)+1 == npLen && n.path[len(path)] == '/' &&
			strings.EqualFold(path[1:], n.path[1:len(path)]) && n.handler != nil {
			return append(ciPath, n.path...)
		}
	}
	return nil
}

func cleanPath(p string) string {
	const stackBufSize = 128
	if p == "" {
		return "/"
	}

	// Reasonably sized buffer on stack to avoid allocations in the common case.
	// If a larger buffer is required, it gets allocated dynamically.
	buf := make([]byte, 0, stackBufSize)

	n := len(p)

	// Invariants:
	//      reading from path; r is index of next byte to process.
	//      writing to buf; w is index of next byte to write.

	// path must start with '/'
	r := 1
	w := 1

	if p[0] != '/' {
		r = 0

		if n+1 > stackBufSize {
			buf = make([]byte, n+1)
		} else {
			buf = buf[:n+1]
		}
		buf[0] = '/'
	}

	trailing := n > 1 && p[n-1] == '/'

	// A bit more clunky without a 'lazybuf' like the path package, but the loop
	// gets completely inlined (bufApp calls).
	// So in contrast to the path package this loop has no expensive function
	// calls (except make, if needed).

	for r < n {
		switch {
		case p[r] == '/':
			// empty path element, trailing slash is added after the end
			r++

		case p[r] == '.' && r+1 == n:
			trailing = true
			r++

		case p[r] == '.' && p[r+1] == '/':
			// . element
			r += 2

		case p[r] == '.' && p[r+1] == '.' && (r+2 == n || p[r+2] == '/'):
			// .. element: remove to last /
			r += 3

			if w > 1 {
				// can backtrack
				w--

				if len(buf) == 0 {
					for w > 1 && p[w] != '/' {
						w--
					}
				} else {
					for w > 1 && buf[w] != '/' {
						w--
					}
				}
			}

		default:
			// Real path element.
			// Add slash if needed
			if w > 1 {
				bufApp(&buf, p, w, '/')
				w++
			}

			// Copy element
			for r < n && p[r] != '/' {
				bufApp(&buf, p, w, p[r])
				w++
				r++
			}
		}
	}

	// Re-append trailing slash
	if trailing && w > 1 {
		bufApp(&buf, p, w, '/')
		w++
	}

	// If the original string was not modified (or only shortened at the end),
	// return the respective substring of the original string.
	// Otherwise return a new string from the buffer.
	if len(buf) == 0 {
		return p[:w]
	}
	return string(buf[:w])
}

// Internal helper to lazily create a buffer if necessary.
// Calls to this function get inlined.
func bufApp(buf *[]byte, s string, w int, c byte) {
	b := *buf
	if len(b) == 0 {
		// No modification of the original string so far.
		// If the next character is the same as in the original string, we do
		// not yet have to allocate a buffer.
		if s[w] == c {
			return
		}

		// Otherwise use either the stack buffer, if it is large enough, or
		// allocate a new buffer on the heap, and copy all previous characters.
		if l := len(s); l > cap(b) {
			*buf = make([]byte, len(s))
		} else {
			*buf = (*buf)[:l]
		}
		b = *buf

		copy(b, s[:w])
	}
	b[w] = c
}

func min(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

func longestCommonPrefix(a, b string) int {
	i := 0
	max := len(a)
	if len(b) < max {
		max = len(b)
	}
	// Use unsafe.Pointer for faster access
	for i < max && a[i] == b[i] {
		i++
	}
	return i
}

func countParams(path string) uint16 {
	var n uint16
	b := []byte(path)
	l := len(b)
	for i := 0; i < l; i++ {
		c := b[i]
		if c == ':' || c == '*' {
			n++
		}
	}
	return n
}
