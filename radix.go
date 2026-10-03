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
	isWild    bool
	origines  string
}

// addPath adds a node with the given handler to the path.
// Not concurrency-safe!
func (n *node) addPath(path string, handler Handler, origines ...string) {
	// The root node never carries a path of its own: the tree is built segment
	// by segment by insertChild, so this is the only reachable branch. The
	// byte-level radix walk that used to follow it, inherited from httprouter,
	// could never run and was removed along with longestCommonPrefix.
	n.insertChild(path, handler, origines...)
	n.nType = root
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
			current.children = append(current.children, child)
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

	for i := 0; i <= pathLen; i++ {
		if i == pathLen || pathBytes[i] == '/' {
			end := i
			segment := path[start:end] // Direct slice, no buffer needed

			// Try static nodes first
			var next *node
			for _, child := range current.children {
				if child.nType == static && child.path == segment {
					next = child
					break
				}
			}

			// Try parameter nodes.
			//
			// An empty segment is skipped on purpose: it is what a trailing
			// slash produces, and matching it would make /users/ a hit for
			// /users/:id with an empty id, which would in turn keep the
			// trailing-slash redirect from ever running.
			if next == nil && segment != "" {
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

			// Try catchAll. A wildcard consumes the rest of the path, so an empty
			// capture is valid here: /files/ matches /files/*path with an empty
			// value, while /files does not match at all, since there is no slash
			// left to consume. Unlike a :param, which never receives an empty value.
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
