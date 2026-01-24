package jsonencdec

import (
	"encoding/json"
	"io"
)

// JsonLib defines the functions needed for a JSON library
type JsonLib struct {
	Marshal       func(v interface{}) ([]byte, error)
	Unmarshal     func(data []byte, v interface{}) error
	MarshalIndent func(x interface{}, prefix string, indent string) ([]byte, error)
	NewDecoder    func(r io.Reader) Decoder
	NewEncoder    func(w io.Writer) Encoder
}

// Example usage:
// jsonencdec.SetDefaultJsonLib(jsonencdec.JsonLib{
//     Marshal:       sonic.Marshal,
//     Unmarshal:     sonic.Unmarshal,
//     MarshalIndent: sonic.MarshalIndent,
//     NewDecoder:    func(r io.Reader) jsonencdec.Decoder { return sonic.ConfigDefault.NewDecoder(r) },
//     NewEncoder:    func(w io.Writer) jsonencdec.Encoder { return sonic.ConfigDefault.NewEncoder(w) },
// })

type Decoder interface {
	Decode(v interface{}) error
}

type Encoder interface {
	Encode(v interface{}) error
}

var (
	DefaultMarshal       func(v interface{}) ([]byte, error)                               = json.Marshal
	DefaultMarshalIndent func(x interface{}, prefix string, indent string) ([]byte, error) = json.MarshalIndent
	DefaultUnmarshal     func(data []byte, v interface{}) error                            = json.Unmarshal
	DefaultNewDecoder    func(r io.Reader) Decoder                                         = func(r io.Reader) Decoder { return json.NewDecoder(r) }
	DefaultNewEncoder    func(w io.Writer) Encoder                                         = func(w io.Writer) Encoder { return json.NewEncoder(w) }
)

// SetDefaultJsonLib sets the default JSON functions using a JsonLib struct
func SetDefaultJsonLib(lib JsonLib) {
	if lib.Marshal != nil {
		DefaultMarshal = lib.Marshal
	}
	if lib.MarshalIndent != nil {
		DefaultMarshalIndent = lib.MarshalIndent
	}
	if lib.Unmarshal != nil {
		DefaultUnmarshal = lib.Unmarshal
	}
	if lib.NewDecoder != nil {
		DefaultNewDecoder = lib.NewDecoder
	}
	if lib.NewEncoder != nil {
		DefaultNewEncoder = lib.NewEncoder
	}
}
