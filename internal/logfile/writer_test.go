package logfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatesDuringWrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	w, err := Open(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err = w.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(p + ".1")
	if err != nil || string(old) != "12345678" {
		t.Fatalf("old=%q err=%v", old, err)
	}
	cur, _ := os.ReadFile(p)
	if string(cur) != "abcdef" {
		t.Fatalf("current=%q", cur)
	}
}
