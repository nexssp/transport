package codec_test

import (
	"bytes"
	"testing"

	"github.com/nexssp/transport/codec"
)

type SampleDTO struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestJSONCodec_MarshalUnmarshal(t *testing.T) {
	t.Parallel()

	c := codec.Default
	if c.Name() != "json" || c.ContentType() != "application/json" {
		t.Fatalf("unexpected codec metadata: %s (%s)", c.Name(), c.ContentType())
	}

	orig := SampleDTO{Name: "Nexss", Age: 42}
	data, err := c.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded SampleDTO
	if err := c.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded != orig {
		t.Fatalf("expected %+v, got %+v", orig, decoded)
	}
}

func TestJSONCodec_StreamEncoderDecoder(t *testing.T) {
	t.Parallel()

	c := codec.JSON{}
	var buf bytes.Buffer

	enc := c.NewEncoder(&buf)
	if err := enc.Encode(SampleDTO{Name: "StreamItem", Age: 10}); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	var out SampleDTO
	dec := c.NewDecoder(&buf)
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if out.Name != "StreamItem" || out.Age != 10 {
		t.Fatalf("unexpected stream decoded struct: %+v", out)
	}
}
