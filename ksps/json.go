package ksps

import (
	"strconv"
	"sync"

	"github.com/kamalshkeir/ksmux/jsonencdec"
)

var kspsBufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 0, 1024)
		return &b
	},
}

// MarshalKSMUXTo appends the JSON serialization to the provided buffer (zero-copy)
// This avoids allocations by reusing the caller's buffer
func (m *WsMessage) MarshalKSMUXTo(buf []byte) []byte {
	buf = append(buf, `{"action":"`...)
	buf = append(buf, m.Action...)
	buf = append(buf, '"')

	if m.Topic != "" {
		buf = append(buf, `,"topic":"`...)
		buf = append(buf, m.Topic...)
		buf = append(buf, '"')
	}
	if m.From != "" {
		buf = append(buf, `,"from":"`...)
		buf = append(buf, m.From...)
		buf = append(buf, '"')
	}
	if m.To != "" {
		buf = append(buf, `,"to":"`...)
		buf = append(buf, m.To...)
		buf = append(buf, '"')
	}
	if m.ID != "" {
		buf = append(buf, `,"id":"`...)
		buf = append(buf, m.ID...)
		buf = append(buf, '"')
	}
	if m.Error != "" {
		buf = append(buf, `,"error":"`...)
		buf = append(buf, m.Error...)
		buf = append(buf, '"')
	}
	if m.AckID != "" {
		buf = append(buf, `,"ack_id":"`...)
		buf = append(buf, m.AckID...)
		buf = append(buf, '"')
	}

	if m.Data != nil {
		buf = append(buf, `,"data":`...)
		buf = appendValueFast(buf, m.Data)
	}

	if len(m.Status) > 0 {
		buf = append(buf, `,"status":{`...)
		first := true
		for k, v := range m.Status {
			if !first {
				buf = append(buf, ',')
			}
			buf = append(buf, '"')
			buf = append(buf, k...)
			buf = append(buf, `":`...)
			if v {
				buf = append(buf, "true"...)
			} else {
				buf = append(buf, "false"...)
			}
			first = false
		}
		buf = append(buf, '}')
	}

	if len(m.Responses) > 0 {
		buf = append(buf, `,"responses":{`...)
		first := true
		for k, v := range m.Responses {
			if !first {
				buf = append(buf, ',')
			}
			buf = append(buf, '"')
			buf = append(buf, k...)
			buf = append(buf, `":{"ack_id":"`...)
			buf = append(buf, v.AckID...)
			buf = append(buf, `","client_id":"`...)
			buf = append(buf, v.ClientID...)
			buf = append(buf, `","success":`...)
			if v.Success {
				buf = append(buf, "true"...)
			} else {
				buf = append(buf, "false"...)
			}
			if v.Error != "" {
				buf = append(buf, `,"error":"`...)
				buf = append(buf, v.Error...)
				buf = append(buf, '"')
			}
			buf = append(buf, '}')
			first = false
		}
		buf = append(buf, '}')
	}

	return append(buf, '}')
}

// MarshalKSMUX sérialise wsMessage avec une gestion ultra-performante
// Optimisé pour éviter les allocations et la réflexion autant que possible
func (m WsMessage) MarshalKSMUX() ([]byte, error) {
	// Get slice pointer from pool
	bufPtr := kspsBufPool.Get().(*[]byte)
	buf := (*bufPtr)[:0]

	buf = append(buf, `{"action":"`...)
	buf = append(buf, m.Action...)
	buf = append(buf, '"')

	// Toutes les fonctions suivantes ajoutent une virgule avant le champ
	if m.Topic != "" {
		buf = append(buf, `,"topic":"`...)
		buf = append(buf, m.Topic...)
		buf = append(buf, '"')
	}
	if m.From != "" {
		buf = append(buf, `,"from":"`...)
		buf = append(buf, m.From...)
		buf = append(buf, '"')
	}
	if m.To != "" {
		buf = append(buf, `,"to":"`...)
		buf = append(buf, m.To...)
		buf = append(buf, '"')
	}
	if m.ID != "" {
		buf = append(buf, `,"id":"`...)
		buf = append(buf, m.ID...)
		buf = append(buf, '"')
	}
	if m.Error != "" {
		buf = append(buf, `,"error":"`...)
		buf = append(buf, m.Error...)
		buf = append(buf, '"')
	}
	if m.AckID != "" {
		buf = append(buf, `,"ack_id":"`...)
		buf = append(buf, m.AckID...)
		buf = append(buf, '"')
	}

	// Data field - fast path for common types
	if m.Data != nil {
		buf = append(buf, `,"data":`...)
		buf = appendValueFast(buf, m.Data)
	}

	// Status map
	if len(m.Status) > 0 {
		buf = append(buf, `,"status":{`...)
		first := true
		for k, v := range m.Status {
			if !first {
				buf = append(buf, ',')
			}
			buf = append(buf, '"')
			buf = append(buf, k...)
			buf = append(buf, `":`...)
			if v {
				buf = append(buf, "true"...)
			} else {
				buf = append(buf, "false"...)
			}
			first = false
		}
		buf = append(buf, '}')
	}

	// Responses map
	if len(m.Responses) > 0 {
		buf = append(buf, `,"responses":{`...)
		first := true
		for k, v := range m.Responses {
			if !first {
				buf = append(buf, ',')
			}
			buf = append(buf, '"')
			buf = append(buf, k...)
			buf = append(buf, `":{"ack_id":"`...)
			buf = append(buf, v.AckID...)
			buf = append(buf, `","client_id":"`...)
			buf = append(buf, v.ClientID...)
			buf = append(buf, `","success":`...)
			if v.Success {
				buf = append(buf, "true"...)
			} else {
				buf = append(buf, "false"...)
			}
			if v.Error != "" {
				buf = append(buf, `,"error":"`...)
				buf = append(buf, v.Error...)
				buf = append(buf, '"')
			}
			buf = append(buf, '}')
			first = false
		}
		buf = append(buf, '}')
	}

	buf = append(buf, '}')

	// Copy result to return (pool buffer will be reused)
	res := make([]byte, len(buf))
	copy(res, buf)

	// Return buffer to pool
	*bufPtr = buf[:0]
	kspsBufPool.Put(bufPtr)

	return res, nil
}

// appendValueFast appends a value to JSON buffer without reflection for common types
func appendValueFast(buf []byte, v interface{}) []byte {
	switch val := v.(type) {
	case nil:
		return append(buf, "null"...)
	case string:
		buf = append(buf, '"')
		buf = appendEscaped(buf, val)
		return append(buf, '"')
	case []byte:
		buf = append(buf, '"')
		buf = appendEscaped(buf, string(val))
		return append(buf, '"')
	case int:
		return strconv.AppendInt(buf, int64(val), 10)
	case int64:
		return strconv.AppendInt(buf, val, 10)
	case int32:
		return strconv.AppendInt(buf, int64(val), 10)
	case uint:
		return strconv.AppendUint(buf, uint64(val), 10)
	case uint64:
		return strconv.AppendUint(buf, val, 10)
	case uint32:
		return strconv.AppendUint(buf, uint64(val), 10)
	case float64:
		return strconv.AppendFloat(buf, val, 'f', -1, 64)
	case float32:
		return strconv.AppendFloat(buf, float64(val), 'f', -1, 32)
	case bool:
		if val {
			return append(buf, "true"...)
		}
		return append(buf, "false"...)
	case map[string]any:
		buf = append(buf, '{')
		first := true
		for k, vv := range val {
			if !first {
				buf = append(buf, ',')
			}
			buf = append(buf, '"')
			buf = appendEscaped(buf, k)
			buf = append(buf, `":`...)
			buf = appendValueFast(buf, vv)
			first = false
		}
		return append(buf, '}')
	case []any:
		buf = append(buf, '[')
		for i, vv := range val {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendValueFast(buf, vv)
		}
		return append(buf, ']')
	case []string:
		buf = append(buf, '[')
		for i, s := range val {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = append(buf, '"')
			buf = appendEscaped(buf, s)
			buf = append(buf, '"')
		}
		return append(buf, ']')
	default:
		// Fallback to reflection-based marshaling for complex types
		if by, err := jsonencdec.DefaultMarshal(val); err == nil {
			return append(buf, by...)
		}
		return append(buf, "null"...)
	}
}

// appendEscaped appends an escaped JSON string value
func appendEscaped(buf []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			buf = append(buf, '\\', '"')
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		case '\t':
			buf = append(buf, '\\', 't')
		default:
			if c < 32 {
				buf = append(buf, '\\', 'u', '0', '0', hexChar(c>>4), hexChar(c&0xF))
			} else {
				buf = append(buf, c)
			}
		}
	}
	return buf
}

func hexChar(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + b - 10
}

// UnmarshalKSMUX - On délègue à la lib par défaut car c'est 100% robuste.
func (m *WsMessage) UnmarshalKSMUX(data []byte) error {
	return jsonencdec.DefaultUnmarshal(data, m)
}
