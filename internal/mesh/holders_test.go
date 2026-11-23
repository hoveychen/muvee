package mesh

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHolderMemoryPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	m := newHolderMemory(dir)
	if _, ok := m.lookup("muvee-mesh", "net1"); ok {
		t.Fatal("fresh memory must be empty")
	}
	rec := foreignHolder{Network: "muvee-mesh", Holder: "yipu-agent", Stack: "compose project yipu", SeenAt: time.Unix(1700000000, 0).UTC()}
	if err := m.remember("net1", rec); err != nil {
		t.Fatal(err)
	}
	// A new process (agent container recreated, DATA_DIR volume kept).
	m2 := newHolderMemory(dir)
	got, ok := m2.lookup("muvee-mesh", "net1")
	if !ok || got != rec {
		t.Fatalf("after restart: got %+v ok=%v, want %+v", got, ok, rec)
	}
	if _, err := os.Stat(filepath.Join(dir, holderFileName)); err != nil {
		t.Errorf("state file: %v", err)
	}
}

// Recreating the network (new ID under the same name) drops the record, also
// from disk; records for other networks stay.
func TestHolderMemoryExpiresWhenNetworkRecreated(t *testing.T) {
	dir := t.TempDir()
	m := newHolderMemory(dir)
	_ = m.remember("old", foreignHolder{Network: "muvee-mesh", Holder: "a"})
	_ = m.remember("other", foreignHolder{Network: "muvee-mesh-b", Holder: "b"})
	if _, ok := m.lookup("muvee-mesh", "new"); ok {
		t.Fatal("record of the old network ID must not apply to the recreated network")
	}
	m2 := newHolderMemory(dir)
	if _, ok := m2.lookup("muvee-mesh", "old"); ok {
		t.Error("stale record should have been pruned from disk")
	}
	if _, ok := m2.lookup("muvee-mesh-b", "other"); !ok {
		t.Error("record of an unrelated network must survive")
	}
}

func TestHolderMemoryCorruptFileAndNoDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, holderFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newHolderMemory(dir)
	if _, ok := m.lookup("muvee-mesh", "x"); ok {
		t.Error("corrupt file must read as empty")
	}
	if err := m.remember("x", foreignHolder{Network: "muvee-mesh"}); err != nil {
		t.Fatalf("remember must overwrite a corrupt file: %v", err)
	}
	if _, ok := newHolderMemory(dir).lookup("muvee-mesh", "x"); !ok {
		t.Error("record lost after rewriting a corrupt file")
	}

	mem := newHolderMemory("") // no DATA_DIR: in-process only
	_ = mem.remember("y", foreignHolder{Network: "n"})
	if _, ok := mem.lookup("n", "y"); !ok {
		t.Error("in-memory record lost")
	}
	var nilMem *holderMemory
	if _, ok := nilMem.lookup("n", "y"); ok {
		t.Error("nil memory must be empty")
	}
}

// An unwritable DATA_DIR still remembers in-process and says so.
func TestHolderMemoryUnwritableDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(dir, nil, 0o644); err != nil { // a file where a dir is expected
		t.Fatal(err)
	}
	m := newHolderMemory(dir)
	if err := m.remember("x", foreignHolder{Network: "n"}); err == nil {
		t.Error("want an error when the state cannot be persisted")
	}
	if _, ok := m.lookup("n", "x"); !ok {
		t.Error("record must be kept in memory even if persisting failed")
	}
}
