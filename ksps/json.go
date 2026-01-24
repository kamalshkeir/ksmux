package ksps

import (
	"bytes"
	"sync"

	"github.com/kamalshkeir/ksmux/jsonencdec"
)

var kspsBufPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

// MarshalKSMUX sérialise wsMessage proprement avec une gestion robuste des virgules.
func (m WsMessage) MarshalKSMUX() ([]byte, error) {
	buf := kspsBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer kspsBufPool.Put(buf)

	buf.WriteString(`{"action":"`)
	buf.WriteString(m.Action)
	buf.WriteByte('"')

	// Toutes les fonctions suivantes ajoutent une virgule avant le champ
	if m.Topic != "" {
		buf.WriteString(`,"topic":"`)
		buf.WriteString(m.Topic)
		buf.WriteByte('"')
	}
	if m.From != "" {
		buf.WriteString(`,"from":"`)
		buf.WriteString(m.From)
		buf.WriteByte('"')
	}
	if m.To != "" {
		buf.WriteString(`,"to":"`)
		buf.WriteString(m.To)
		buf.WriteByte('"')
	}
	if m.ID != "" {
		buf.WriteString(`,"id":"`)
		buf.WriteString(m.ID)
		buf.WriteByte('"')
	}
	if m.Error != "" {
		buf.WriteString(`,"error":"`)
		buf.WriteString(m.Error)
		buf.WriteByte('"')
	}
	if m.AckID != "" {
		buf.WriteString(`,"ack_id":"`)
		buf.WriteString(m.AckID)
		buf.WriteByte('"')
	}

	if m.Data != nil {
		if by, err := jsonencdec.DefaultMarshal(m.Data); err == nil {
			buf.WriteString(`,"data":`)
			buf.Write(by)
		}
	}
	if m.Status != nil {
		if by, err := jsonencdec.DefaultMarshal(m.Status); err == nil {
			buf.WriteString(`,"status":`)
			buf.Write(by)
		}
	}
	if m.Responses != nil {
		if by, err := jsonencdec.DefaultMarshal(m.Responses); err == nil {
			buf.WriteString(`,"responses":`)
			buf.Write(by)
		}
	}

	buf.WriteByte('}')

	// Copie pour éviter les conflits dans le pool
	res := make([]byte, buf.Len())
	copy(res, buf.Bytes())
	return res, nil
}

// UnmarshalKSMUX - On délègue à la lib par défaut car c'est 100% robuste.
func (m *WsMessage) UnmarshalKSMUX(data []byte) error {
	return jsonencdec.DefaultUnmarshal(data, m)
}
