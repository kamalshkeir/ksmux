package ws

import (
	"github.com/kamalshkeir/ksmux/jsonencdec"
)

type ksmuxFastEncoder interface {
	MarshalKSMUX() ([]byte, error)
}

func (c *Conn) WriteJSON(v interface{}) error {
	if m, ok := v.(ksmuxFastEncoder); ok {
		data, err := m.MarshalKSMUX()
		if err == nil {
			return c.WriteMessageDirect(TextMessage, data)
		}
	}
	data, err := jsonencdec.DefaultMarshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessageDirect(TextMessage, data)
}

func (c *Conn) ReadJSON(v interface{}) error {
	_, r, err := c.NextReader()
	if err != nil {
		return err
	}
	return jsonencdec.DefaultNewDecoder(r).Decode(v)
}

func ReadJSONFromBytes(data []byte, v interface{}) error {
	return jsonencdec.DefaultUnmarshal(data, v)
}

func WriteJSONToBytes(v interface{}) ([]byte, error) {
	if m, ok := v.(ksmuxFastEncoder); ok {
		return m.MarshalKSMUX()
	}
	return jsonencdec.DefaultMarshal(v)
}
