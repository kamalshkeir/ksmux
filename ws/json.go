// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ws

import (
	"io"

	"github.com/kamalshkeir/ksmux/jsonencdec"
)

// writeJSON writes the JSON encoding of v as a message.
func (c *Conn) writeJSON(v interface{}) error {
	w, err := c.NextWriter(TextMessage)
	if err != nil {
		return err
	}
	defer w.Close()

	by, err1 := jsonencdec.DefaultMarshal(v)
	if err1 != nil {
		return err1
	}
	_, err2 := w.Write(by)
	if err2 != nil {
		return err2
	}
	return nil
}

// ReadJSON reads the next JSON-encoded message from the connection and stores
// it in the value pointed to by v.
func ReadJSON(c *Conn, v interface{}) error {
	return c.ReadJSON(v)
}

// ReadJSON reads the next JSON-encoded message from the connection and stores
func (c *Conn) ReadJSON(v interface{}) error {
	_, r, err := c.NextReader()
	if err != nil {
		return err
	}
	by, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	err = jsonencdec.DefaultUnmarshal(by, v)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}
