// Package terminal implements PTY-backed sessions and the binary frame codec
// used between the daemon and its web clients.
package terminal

import (
	"encoding/binary"
	"errors"
)

// Frame opcodes. 0x01-0x04 mirror the terminal frame vocabulary Paseo uses so
// the design carries over to the relay path unchanged. Control opcodes live at
// 0x10+ to leave room for future terminal verbs.
const (
	OpOutput   byte = 0x01 // server -> client: raw PTY bytes
	OpInput    byte = 0x02 // client -> server: keyboard bytes
	OpResize   byte = 0x03 // client -> server: rows, cols (uint16 big-endian)
	OpSnapshot byte = 0x04 // server -> client: replay buffer to rebuild the screen

	OpCreate   byte = 0x10 // client -> server: JSON CreateRequest
	OpClose    byte = 0x11 // client -> server: terminate the session
	OpAttached byte = 0x12 // server -> client: session id (UTF-8)
	OpExit     byte = 0x13 // server -> client: exit code (int32 big-endian)
	OpError    byte = 0x14 // server -> client: error text (UTF-8)
	OpRename   byte = 0x15 // client -> server: new display name (UTF-8)
	OpName     byte = 0x16 // server -> client: current display name (UTF-8)
)

// HeaderSize is the fixed prefix: 1 byte opcode + 2 bytes slot.
const HeaderSize = 3

// MaxFrameBytes caps an inbound or outbound frame.
const MaxFrameBytes = 32 * 1024 * 1024

// ErrShortFrame is returned when a frame is smaller than the header.
var ErrShortFrame = errors.New("terminal: frame too short")

// Frame is one multiplexed terminal message.
type Frame struct {
	Op   byte
	Slot uint16
	Data []byte
}

// Encode serializes a frame as [opcode][slot hi][slot lo][payload].
func Encode(f Frame) []byte {
	out := make([]byte, HeaderSize+len(f.Data))
	out[0] = f.Op
	binary.BigEndian.PutUint16(out[1:3], f.Slot)
	copy(out[HeaderSize:], f.Data)
	return out
}

// Decode parses a frame produced by Encode.
func Decode(b []byte) (Frame, error) {
	if len(b) < HeaderSize {
		return Frame{}, ErrShortFrame
	}
	return Frame{
		Op:   b[0],
		Slot: binary.BigEndian.Uint16(b[1:3]),
		Data: b[HeaderSize:],
	}, nil
}

// ResizePayload encodes rows and cols for OpResize.
func ResizePayload(rows, cols uint16) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint16(out[0:2], rows)
	binary.BigEndian.PutUint16(out[2:4], cols)
	return out
}

// DecodeResize reads an OpResize payload.
func DecodeResize(b []byte) (rows, cols uint16, ok bool) {
	if len(b) < 4 {
		return 0, 0, false
	}
	return binary.BigEndian.Uint16(b[0:2]), binary.BigEndian.Uint16(b[2:4]), true
}

// ExitPayload encodes a process exit code for OpExit.
func ExitPayload(code int) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(int32(code)))
	return out
}

// DecodeExit reads an OpExit payload.
func DecodeExit(b []byte) (int, bool) {
	if len(b) < 4 {
		return 0, false
	}
	return int(int32(binary.BigEndian.Uint32(b))), true
}

// CreateRequest is the JSON payload of OpCreate.
type CreateRequest struct {
	Slot    uint16 `json:"slot"`
	Session string `json:"session"`
	Name    string `json:"name"`
	Agent   string `json:"agent"`
	Cmd     string `json:"cmd"`
	Cwd     string `json:"cwd"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
}
