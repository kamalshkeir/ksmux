// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ws

import (
	"net/http"
	"time"
)

var FuncBeforeUpgradeWSHandler = func(w http.ResponseWriter, r *http.Request) {
}

var DefaultUpgraderKSMUX = Upgrader{
	EnableCompression: true,
	ReadBufferSize:    1024,
	WriteBufferSize:   1024,
	HandshakeTimeout:  10 * time.Second,
}

func UpgradeConnection(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*Conn, error) {
	return DefaultUpgraderKSMUX.Upgrade(w, r, responseHeader)
}
