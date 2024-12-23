// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ws

import (
	"io"

	"github.com/kamalshkeir/kencoding/json"
)

// WriteJSON writes the JSON encoding of v as a message.
//
// Deprecated: Use c.WriteJSON instead.
func WriteJSON(c *Conn, v interface{}) error {
	return c.WriteJSON(v)
}

// WriteJSON writes the JSON encoding of v as a message.
//
// See the documentation for kencoding/json Marshal for details about the
// conversion of Go values to JSON.
func (c *Conn) WriteJSON(v interface{}) error {
	err := c.writeJSON(v)
	if err != nil {
		return err
	}
	return nil
}

func (c *Conn) writeJSON(v interface{}) error {
	w, err := c.NextWriter(TextMessage)
	if err != nil {
		return err
	}

	err1 := json.NewEncoder(w).Encode(v)
	if err1 != nil {
		return err1
	}
	err2 := w.Close()
	if err2 != nil {
		return err2
	}
	return nil
}

// ReadJSON reads the next JSON-encoded message from the connection and stores
// it in the value pointed to by v.
//
// Deprecated: Use c.ReadJSON instead.
func ReadJSON(c *Conn, v interface{}) error {
	return c.ReadJSON(v)
}

// ReadJSON reads the next JSON-encoded message from the connection and stores
// it in the value pointed to by v.
//
// See the documentation for the kencoding/json Unmarshal function for details
// about the conversion of JSON to a Go value.
func (c *Conn) ReadJSON(v interface{}) error {
	_, r, err := c.NextReader()
	if err != nil {
		return err
	}
	err = json.NewDecoder(r).Decode(v)
	if err == io.EOF {
		// One value is expected in the message.
		err = io.ErrUnexpectedEOF
	}
	return err
}
