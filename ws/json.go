// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ws

import (
	"sync"

	"encoding/json"
)

var (
	decoderPool = sync.Pool{
		New: func() interface{} {
			return json.NewDecoder(nil)
		},
	}
)

// WriteJSON writes the JSON encoding of v as a message through the actor
func (c *Conn) WriteJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return c.WriteMessage(TextMessage, data)
}

// ReadJSON reads the next JSON-encoded message from the connection and stores
func (c *Conn) ReadJSON(v interface{}) error {
	_, r, err := c.NextReader()
	if err != nil {
		return err
	}

	dec := decoderPool.Get().(*json.Decoder)
	dec = json.NewDecoder(r)
	err = dec.Decode(v)
	decoderPool.Put(dec)
	return err
}
