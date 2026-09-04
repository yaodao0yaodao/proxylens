package logfile

import (
	"os"
	"path/filepath"
	"sync"
)

type Writer struct {
	mu   sync.Mutex
	path string
	max  int64
	file *os.File
	size int64
}

func Open(path string, max int64) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	w := &Writer{path: path, max: max}
	if info, err := os.Stat(path); err == nil {
		w.size = info.Size()
	}
	if w.max > 0 && w.size >= w.max {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
		w.size = 0
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	w.file = f
	return w, err
}
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.max > 0 && w.size+int64(len(p)) > w.max {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}
func (w *Writer) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	_ = os.Remove(w.path + ".1")
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	return nil
}
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}
