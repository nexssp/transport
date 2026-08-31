package codec

import (
	"encoding/json"
	"io"
)

// Codec defines serialization and deserialization across all Nexss transports.
type Codec interface {
	Name() string
	ContentType() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
	NewDecoder(r io.Reader) Decoder
	NewEncoder(w io.Writer) Encoder
}

type Decoder interface {
	Decode(v any) error
}

type Encoder interface {
	Encode(v any) error
}

// JSON is the default standard library JSON codec.
type JSON struct{}

func (JSON) Name() string                       { return "json" }
func (JSON) ContentType() string                { return "application/json" }
func (JSON) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (JSON) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
func (JSON) NewDecoder(r io.Reader) Decoder     { return json.NewDecoder(r) }
func (JSON) NewEncoder(w io.Writer) Encoder     { return json.NewEncoder(w) }

// Default is the fallback JSON codec used across all transports.
var Default Codec = JSON{}
