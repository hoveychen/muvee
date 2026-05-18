package agentcontrol

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestFrameRoundTripJSON proves every defined frame type marshals and
// unmarshals losslessly. If anyone adds a Frame field or Type constant, this
// test should grow to cover it.
func TestFrameRoundTripJSON(t *testing.T) {
	cases := []struct {
		name string
		f    Frame
	}{
		{"hello", Frame{Type: TypeHello, NodeID: "11111111-2222-3333-4444-555555555555"}},
		{"ping", Frame{Type: TypePing}},
		{"pong", Frame{Type: TypePong}},
		{"open_exec_with_size", Frame{
			Type:      TypeOpenExec,
			Session:   "s-1",
			Container: "muvee-foo",
			Cmd:       []string{"echo", "hello"},
			Cols:      120,
			Rows:      40,
		}},
		{"stdio_stdin", Frame{
			Type:    TypeStdio,
			Session: "s-1",
			Stream:  StreamStdin,
			Data:    []byte{0x03}, // Ctrl-C byte
		}},
		{"stdio_stdout", Frame{
			Type:    TypeStdio,
			Session: "s-1",
			Stream:  StreamStdout,
			Data:    []byte("hello\r\n"),
		}},
		{"stdio_stderr_binary", Frame{
			Type:    TypeStdio,
			Session: "s-1",
			Stream:  StreamStderr,
			Data:    []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0xfd},
		}},
		{"resize", Frame{Type: TypeResize, Session: "s-1", Cols: 200, Rows: 60}},
		{"signal", Frame{Type: TypeSignal, Session: "s-1", Sig: "SIGTERM"}},
		{"exit_zero", Frame{Type: TypeExit, Session: "s-1", Code: 0}},
		{"exit_nonzero", Frame{Type: TypeExit, Session: "s-1", Code: 42}},
		{"error", Frame{Type: TypeError, Session: "s-1", Msg: "container not found"}},
		{"close", Frame{Type: TypeClose, Session: "s-1"}},
		{"open_cp_up", Frame{Type: TypeOpenCp, Session: "s-1", Container: "muvee-foo", Path: "/app/in/", Direction: CpDirectionUp}},
		{"open_cp_down", Frame{Type: TypeOpenCp, Session: "s-1", Container: "muvee-foo", Path: "/app/out.tar", Direction: CpDirectionDown}},
		{"cp_up_tar", Frame{Type: TypeCpUpTar, Session: "s-1", Data: []byte("tar bytes here")}},
		{"cp_down_tar", Frame{Type: TypeCpDownTar, Session: "s-1", Data: []byte("tar bytes back")}},
		{"cp_end", Frame{Type: TypeCpEnd, Session: "s-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.f)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got Frame
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, tc.f) {
				t.Fatalf("round-trip mismatch\n got: %#v\nwant: %#v\n raw: %s", got, tc.f, raw)
			}
		})
	}
}

// TestFrameOmitsEmptyFields keeps the wire size tight: a tiny frame must not
// drag along default-zero values for fields it doesn't use.
func TestFrameOmitsEmptyFields(t *testing.T) {
	f := Frame{Type: TypePing}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"type":"ping"}` {
		t.Fatalf("ping frame should marshal as {\"type\":\"ping\"}, got %s", raw)
	}
}

// TestFrameStdioPreservesBinary catches a regression where switching to a
// different encoder might lose high bytes (e.g. UTF-8 replacement). Cursor
// position queries `\x1b[6n` and ANSI color codes both have 0x1b which a
// naive impl might mangle.
func TestFrameStdioPreservesBinary(t *testing.T) {
	payload := []byte{0x1b, '[', '3', '1', 'm', 'r', 'e', 'd', 0x1b, '[', '0', 'm'}
	f := Frame{Type: TypeStdio, Session: "s", Stream: StreamStdout, Data: payload}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Frame
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got.Data, payload) {
		t.Fatalf("binary preservation lost\n got: %v\nwant: %v\n raw: %s", got.Data, payload, raw)
	}
}
