// Copyright 2020 Kamal SHKEIR. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package ksmux

import (
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kamalshkeir/klog"
)

type typeOfNode uint8

const (
	static typeOfNode = iota // default
	root
	param
	catchAll
)

type node struct {
	path     string
	indexes  string
	handler  Handler
	children []*node
	isWild   bool
	nType    typeOfNode
	priority uint32
}

// Increments priority of the given child and reorders if necessary
func (n *node) incrementPrio(pos int) int {
	cs := n.children
	cs[pos].priority++
	prio := cs[pos].priority

	newPos := pos
	for ; newPos > 0 && cs[newPos-1].priority < prio; newPos-- {
		cs[newPos-1], cs[newPos] = cs[newPos], cs[newPos-1]
	}

	if newPos != pos {
		n.indexes = n.indexes[:newPos] + n.indexes[pos:pos+1] + n.indexes[newPos:pos] + n.indexes[pos+1:]
	}

	return newPos
}

// addPath adds a node with the given handler to the path.
// Not concurrency-safe!
func (n *node) addPath(path string, handler Handler) {
	fullPath := path
	n.priority++

	// Empty tree
	if n.path == "" && n.indexes == "" {
		n.insertChild(path, fullPath, handler)
		n.nType = root
		return
	}

walk:
	for {
		// Find the longest common prefix.
		// This also implies that the common prefix contains no ':' or '*'
		// since the existing key can't contain those chars.
		i := longestCommonPrefix(path, n.path)

		// Split edge
		if i < len(n.path) {
			child := node{
				path:     n.path[i:],
				isWild:   n.isWild,
				nType:    static,
				indexes:  n.indexes,
				children: n.children,
				handler:  n.handler,
				priority: n.priority - 1,
			}

			n.children = []*node{&child}
			// []byte for proper unicode char conversion, see #65
			n.indexes = string([]byte{n.path[i]})
			n.path = path[:i]
			n.handler = nil
			n.isWild = false
		}

		// Make new node a child of this node
		if i < len(path) {
			path = path[i:]

			if n.isWild {
				n = n.children[0]
				n.priority++

				// Check if the wildcard matches
				if len(path) >= len(n.path) && n.path == path[:len(n.path)] &&
					// Adding a child to a catchAll is not possible
					n.nType != catchAll &&
					// Check for longer wildcard, e.g. :name and :names
					(len(n.path) >= len(path) || path[len(n.path)] == '/') {
					continue walk
				} else {
					// Wildcard conflict
					pathSeg := path
					if n.nType != catchAll {
						pathSeg = strings.SplitN(pathSeg, "/", 2)[0]
					}
					prefix := fullPath[:strings.Index(fullPath, pathSeg)] + n.path
					klog.Printf("rd'%s' in new path'%s' conflicts with existing wildcard '%s' with prefix '%s' \n", pathSeg, fullPath, n.path, prefix)
					os.Exit(1)
				}
			}

			idxc := path[0]

			// '/' after param
			if n.nType == param && idxc == '/' && len(n.children) == 1 {
				n = n.children[0]
				n.priority++
				continue walk
			}

			// Check if a child with the next path byte exists
			for i, c := range []byte(n.indexes) {
				if c == idxc {
					i = n.incrementPrio(i)
					n = n.children[i]
					continue walk
				}
			}

			// Otherwise insert it
			if idxc != ':' && idxc != '*' {
				// []byte for proper unicode char conversion, see #65
				n.indexes += string([]byte{idxc})
				child := &node{}
				n.children = append(n.children, child)
				n.incrementPrio(len(n.indexes) - 1)
				n = child
			}
			n.insertChild(path, fullPath, handler)
			return
		}

		// Otherwise add handler to current node
		if n.handler != nil {
			klog.Printf("rdanother handler is already registered for this path '%s'\n", fullPath)
			os.Exit(1)
		}
		n.handler = handler
		return
	}
}

func (n *node) insertChild(path, fullPath string, handler Handler) {
	for {
		// Find prefix until first wildcard
		wildcard, i, valid := findPathWildcard(path)
		if i < 0 { // No wilcard found
			break
		}

		// The wildcard name must not contain ':' and '*'
		if !valid {
			klog.Printf("rdonly one wildcard per path segment is allowed, has:  '%s' in path '%s'\n", wildcard, fullPath)
			os.Exit(1)
		}

		// Check if the wildcard has a name
		if len(wildcard) < 2 {
			klog.Printf("rdwildcards must be named with a non-empty name in path:  '%s'\n", fullPath)
			os.Exit(1)
		}

		// Check if this node has existing children which would be
		// unreachable if we insert the wildcard here
		if len(n.children) > 0 {
			klog.Printf("rdwildcard segment '%s' conflicts with existing children in path '%s'\n", wildcard, fullPath)
			os.Exit(1)
		}

		// param
		if wildcard[0] == ':' {
			if i > 0 {
				// Insert prefix before the current wildcard
				n.path = path[:i]
				path = path[i:]
			}

			n.isWild = true
			child := &node{
				nType: param,
				path:  wildcard,
			}
			n.children = []*node{child}
			n = child
			n.priority++

			// If the path doesn't end with the wildcard, then there
			// will be another non-wildcard subpath starting with '/'
			if len(wildcard) < len(path) {
				path = path[len(wildcard):]
				child := &node{
					priority: 1,
				}
				n.children = []*node{child}
				n = child
				continue
			}

			// Otherwise we're done. Insert the handler in the new leaf
			n.handler = handler
			return
		}

		// catchAll
		if i+len(wildcard) != len(path) {
			klog.Printf("rdcatch-all routes are only allowed at the end of the path in path '%s'\n", fullPath)
			os.Exit(1)
		}

		if len(n.path) > 0 && n.path[len(n.path)-1] == '/' {
			klog.Printf("rdcatch-all conflicts with existing handler for the path segment root in path '%s'\n", fullPath)
			os.Exit(1)
		}

		// Currently fixed width 1 for '/'
		i--
		if path[i] != '/' {
			klog.Printf("rdno / before catch-all in path '%s'\n", fullPath)
			os.Exit(1)
		}

		n.path = path[:i]

		// First node: catchAll node with empty path
		child := &node{
			isWild: true,
			nType:  catchAll,
		}
		n.children = []*node{child}
		n.indexes = string('/')
		n = child
		n.priority++

		// Second node: node holding the variable
		child = &node{
			path:     path[i:],
			nType:    catchAll,
			handler:  handler,
			priority: 1,
		}
		n.children = []*node{child}

		return
	}

	// If no wildcard was found, simply insert the path and handler
	n.path = path
	n.handler = handler
}

// Returns the handler registered with the given path (key). The values of
// wildcards are saved to a map.
// If no handler can be found, a TSR (trailing slash redirect) recommendation is
// made if a handler exists with an extra (without the) trailing slash for the
// given path.
func (n *node) getHandler(path string, params func() *Params) (handler Handler, ps *Params, tsr bool) {
walk: // Outer loop for walking the tree
	for {
		prefix := n.path
		if len(path) > len(prefix) {
			if path[:len(prefix)] == prefix {
				path = path[len(prefix):]

				// If this node does not have a wildcard (param or catchAll)
				// child, we can just look up the next child node and continue
				// to walk down the tree
				if !n.isWild {
					idxc := path[0]
					for i, c := range []byte(n.indexes) {
						if c == idxc {
							n = n.children[i]
							continue walk
						}
					}

					// Nothing found.
					// We can recommend to redirect to the same URL without a
					// trailing slash if a leaf exists for that path.
					tsr = (path == "/" && n.handler != nil)
					return
				}

				// Handle wildcard child
				n = n.children[0]
				switch n.nType {
				case param:
					// Find param end (either '/' or path end)
					end := 0
					for end < len(path) && path[end] != '/' {
						end++
					}

					// Save param value
					if params != nil {
						if ps == nil {
							ps = params()
						}
						// Expand slice within preallocated capacity
						i := len(*ps)
						*ps = (*ps)[:i+1]
						(*ps)[i] = Param{
							Key:   n.path[1:],
							Value: path[:end],
						}
					}

					// We need to go deeper!
					if end < len(path) {
						if len(n.children) > 0 {
							path = path[end:]
							n = n.children[0]
							continue walk
						}

						// ... but we can't
						tsr = (len(path) == end+1)
						return
					}

					if handler = n.handler; handler != nil {
						return
					} else if len(n.children) == 1 {
						// No handler found. Check if a handler for this path + a
						// trailing slash exists for TSR recommendation
						n = n.children[0]
						tsr = (n.path == "/" && n.handler != nil) || (n.path == "" && n.indexes == "/")
					}

					return

				case catchAll:
					// Save param value
					if params != nil {
						if ps == nil {
							ps = params()
						}
						// Expand slice within preallocated capacity
						i := len(*ps)
						*ps = (*ps)[:i+1]
						(*ps)[i] = Param{
							Key:   n.path[2:],
							Value: path,
						}
					}

					handler = n.handler
					return

				default:
					klog.Printf("rdinvalid node type '%v'\n", n.nType)
					os.Exit(1)
				}
			}
		} else if path == prefix {
			// We should have reached the node containing the handler.
			// Check if this node has a handler registered.
			if handler = n.handler; handler != nil {
				return
			}

			// If there is no handler for this route, but this route has a
			// wildcard child, there must be a handler for this path with an
			// additional trailing slash
			if path == "/" && n.isWild && n.nType != root {
				tsr = true
				return
			}

			if path == "/" && n.nType == static {
				tsr = true
				return
			}

			// No handler found. Check if a handler for this path + a
			// trailing slash exists for trailing slash recommendation
			for i, c := range []byte(n.indexes) {
				if c == '/' {
					n = n.children[i]
					tsr = (len(n.path) == 1 && n.handler != nil) ||
						(n.nType == catchAll && n.children[0].handler != nil)
					return
				}
			}
			return
		}

		// Nothing found. We can recommend to redirect to the same URL with an
		// extra trailing slash if a leaf exists for that path
		tsr = (path == "/") ||
			(len(prefix) == len(path)+1 && prefix[len(path)] == '/' &&
				path == prefix[:len(prefix)-1] && n.handler != nil)
		return
	}
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
				klog.Printf("rdinvalid node type '%v'\n", n.nType)
				os.Exit(1)
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
	max := min(len(a), len(b))
	for i < max && a[i] == b[i] {
		i++
	}
	return i
}

// findPathWildcard find a wildcard segment and check the name for invalid characters.
// Returns -1 as index, if no wildcard was found.
func findPathWildcard(path string) (wilcard string, i int, valid bool) {
	// Find start
	for start, c := range []byte(path) {
		if c != ':' && c != '*' {
			continue
		}
		// check if chars are valid
		valid = true
		for end, c := range []byte(path[start+1:]) {
			switch c {
			case '/':
				return path[start : start+1+end], start, valid
			case ':', '*':
				valid = false
			}
		}
		return path[start:], start, valid
	}
	return "", -1, false
}

func countParams(path string) uint16 {
	var n uint16
	for i := range []byte(path) {
		switch path[i] {
		case ':', '*':
			n++
		}
	}
	return n
}
