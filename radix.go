// Copyright 2013 Julien Schmidt. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package ksmux

import (
	"strconv"
	"strings"
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
	// lg.Debug("addPath called", "path", path)

	if n.path == "" && n.indexes == "" {
		// lg.Debug("empty tree, inserting root")
		n.insertChild(path, handler, origines...)
		n.nType = root
		return
	}

walk:
	for {
		i := longestCommonPrefix(path, n.path)
		// lg.Debug("found common prefix", "len", i, "path", path, "nodePath", n.path)

		if i < len(n.path) {
			// lg.Debug("splitting edge", "path", path, "nodePath", n.path)
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
			// lg.Debug("remaining path", "path", path)

			// Check for parameter
			if path[0] == ':' {
				// lg.Debug("found parameter in path", "path", path)
				for i, index := range []byte(n.indexes) {
					if index == ':' {
						// lg.Debug("found existing param node", "index", i)
						n = n.children[i]
						continue walk
					}
				}
			}

			// Find matching child
			for i, index := range []byte(n.indexes) {
				if path[0] == index {
					// lg.Debug("found matching child", "index", i, "char", string(index))
					n = n.children[i]
					continue walk
				}
			}

			// No matching child
			// lg.Debug("no matching child, creating new one", "path", path)
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
