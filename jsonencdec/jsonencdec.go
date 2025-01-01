package jsonencdec

import (
	"encoding/json"
)

var (
	DefaultMarshal       func(v interface{}) ([]byte, error)                               = json.Marshal
	DefaultMarshalIndent func(x interface{}, prefix string, indent string) ([]byte, error) = json.MarshalIndent
	DefaultUnmarshal     func(data []byte, v interface{}) error                            = json.Unmarshal
)

// SetDefaultJsonLib sets the default JSON marshal/unmarshal/marshalIndent functions
func SetDefaultJsonLib(marshal func(v interface{}) ([]byte, error), unmarshal func(data []byte, v interface{}) error, marshalIndent func(x interface{}, prefix string, indent string) ([]byte, error)) {
	if marshal != nil {
		DefaultMarshal = marshal
	}
	if marshalIndent != nil {
		DefaultMarshalIndent = marshalIndent
	}
	if unmarshal != nil {
		DefaultUnmarshal = unmarshal
	}
}
