package terminal

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	in := Frame{Op: OpOutput, Slot: 0x1234, Data: []byte("hello")}
	out, err := Decode(Encode(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Op != in.Op || out.Slot != in.Slot || !bytes.Equal(out.Data, in.Data) {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}

func TestDecodeShortFrame(t *testing.T) {
	if _, err := Decode([]byte{OpOutput, 0x00}); err == nil {
		t.Fatal("expected error for short frame")
	}
}

func TestResizePayloadRoundTrip(t *testing.T) {
	rows, cols, ok := DecodeResize(ResizePayload(30, 100))
	if !ok || rows != 30 || cols != 100 {
		t.Fatalf("got rows=%d cols=%d ok=%v", rows, cols, ok)
	}
	if _, _, ok := DecodeResize([]byte{0x00}); ok {
		t.Fatal("expected short resize to fail")
	}
}

func TestExitPayloadRoundTrip(t *testing.T) {
	code, ok := DecodeExit(ExitPayload(42))
	if !ok || code != 42 {
		t.Fatalf("got code=%d ok=%v", code, ok)
	}
	code, ok = DecodeExit(ExitPayload(-1))
	if !ok || code != -1 {
		t.Fatalf("got code=%d ok=%v", code, ok)
	}
}
