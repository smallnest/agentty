// Package sessionhost implements a tiny detached PTY host.
//
// A host process owns the PTY and the agent process. The daemon connects to it
// over a Unix domain socket, so restarting the daemon does not kill the agent:
// the new daemon simply dials the same socket and receives the replay buffer.
package sessionhost

import (
	"encoding/binary"
	"errors"
	"io"
)

const maxFrameBytes = 4 * 1024 * 1024

// Frame types.
const (
	TypeInput  byte = 0x01 // daemon -> host
	TypeOutput byte = 0x02 // host -> daemon
	TypeResize byte = 0x03 // daemon -> host
	TypeExit   byte = 0x04 // host -> daemon
	TypeKill   byte = 0x05 // daemon -> host
	TypeReady  byte = 0x06 // host -> daemon
	TypeError  byte = 0x07 // host -> daemon
)

// Frame is one length-prefixed message on the host socket.
type Frame struct {
	Type byte
	Data []byte
}

// ErrFrameTooLarge is returned for a frame over the protocol limit.
var ErrFrameTooLarge = errors.New("sessionhost: frame too large")

func writeFrame(w io.Writer, frame Frame) error {
	if len(frame.Data) > maxFrameBytes {
		return ErrFrameTooLarge
	}
	header := make([]byte, 5)
	header[0] = frame.Type
	binary.BigEndian.PutUint32(header[1:], uint32(len(frame.Data)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(frame.Data) == 0 {
		return nil
	}
	_, err := w.Write(frame.Data)
	return err
}

func readFrame(r io.Reader) (Frame, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > maxFrameBytes {
		return Frame{}, ErrFrameTooLarge
	}
	data := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, data); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Type: header[0], Data: data}, nil
}

// ResizePayload encodes rows and cols.
func ResizePayload(rows, cols uint16) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint16(out[0:2], rows)
	binary.BigEndian.PutUint16(out[2:4], cols)
	return out
}

// DecodeResize reads a resize payload.
func DecodeResize(data []byte) (rows, cols uint16, ok bool) {
	if len(data) < 4 {
		return 0, 0, false
	}
	return binary.BigEndian.Uint16(data[0:2]), binary.BigEndian.Uint16(data[2:4]), true
}

// ExitPayload encodes a process exit code.
func ExitPayload(code int) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(int32(code)))
	return out
}

// DecodeExit reads an exit payload.
func DecodeExit(data []byte) (int, bool) {
	if len(data) < 4 {
		return 0, false
	}
	return int(int32(binary.BigEndian.Uint32(data))), true
}
