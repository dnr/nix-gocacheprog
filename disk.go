package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// indexEntry is the metadata that DiskCache stores on disk for an ActionID.
type indexEntry struct {
	Version   int    `json:"v"`
	OutputID  string `json:"o"`
	Size      int64  `json:"n"`
	TimeNanos int64  `json:"t"`
}

type DiskCache struct {
	Dir         string
	Verbose     bool
	ManualATime bool
}

func (dc *DiskCache) Get(ctx context.Context, actionID string) (outputID, diskPath string, err error) {
	actionFile := filepath.Join(dc.Dir, fmt.Sprintf("a-%s", actionID))
	ij, err := os.ReadFile(actionFile)
	if err != nil {
		if os.IsNotExist(err) {
			err = nil
			if dc.Verbose {
				log.Printf("disk miss: %v", actionID)
			}
		}
		return "", "", err
	}
	dc.markAccess(actionFile)
	var ie indexEntry
	if err := json.Unmarshal(ij, &ie); err != nil {
		log.Printf("Warning: JSON error for action %q: %v", actionID, err)
		return "", "", nil
	}
	if _, err := hex.DecodeString(ie.OutputID); err != nil {
		// Protect against malicious non-hex OutputID on disk
		return "", "", nil
	}
	outputFile := filepath.Join(dc.Dir, fmt.Sprintf("o-%v", ie.OutputID))
	dc.markAccess(outputFile)
	return ie.OutputID, outputFile, nil
}

func (dc *DiskCache) OutputFilename(outputID string) string {
	if len(outputID) < 4 || len(outputID) > 1000 {
		return ""
	}
	for i := range outputID {
		b := outputID[i]
		if b >= '0' && b <= '9' || b >= 'a' && b <= 'f' {
			continue
		}
		return ""
	}
	return filepath.Join(dc.Dir, fmt.Sprintf("o-%s", outputID))
}

func (dc *DiskCache) Put(ctx context.Context, actionID, outputID string, size int64, body io.Reader) (diskPath string, _ error) {
	file := filepath.Join(dc.Dir, fmt.Sprintf("o-%s", outputID))

	// Special case empty files; they're both common and easier to do race-free.
	if size == 0 {
		zf, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return "", err
		}
		zf.Close()
	} else {
		wrote, err := writeAtomic(file, body)
		if err != nil {
			return "", err
		}
		if wrote != size {
			return "", fmt.Errorf("wrote %d bytes, expected %d", wrote, size)
		}
	}

	ij, err := json.Marshal(indexEntry{
		Version:   1,
		OutputID:  outputID,
		Size:      size,
		TimeNanos: time.Now().UnixNano(),
	})
	if err != nil {
		return "", err
	}
	actionFile := filepath.Join(dc.Dir, fmt.Sprintf("a-%s", actionID))
	if _, err := writeAtomic(actionFile, bytes.NewReader(ij)); err != nil {
		return "", err
	}
	return file, nil
}

func (dc *DiskCache) Clean(ttl time.Duration) {
	f, err := os.Open(dc.Dir)
	if err != nil {
		return
	}
	expire := time.Now().Unix() - int64(ttl.Seconds())
	for {
		ents, err := f.ReadDir(1000)
		if errors.Is(err, io.EOF) || len(ents) == 0 {
			break
		}
		for _, ent := range ents {
			if fi, err := ent.Info(); err != nil {
				continue
			} else if st, ok := fi.Sys().(*syscall.Stat_t); !ok {
				continue
			} else if st.Atim.Sec < expire {
				os.Remove(filepath.Join(dc.Dir, fi.Name()))
			}
		}
	}
}

func (dc *DiskCache) markAccess(path string) {
	if !dc.ManualATime {
		return
	}
	var st syscall.Stat_t
	now := time.Now().Unix()
	if syscall.Stat(path, &st) != nil || now-st.Atim.Sec < 86400 {
		return
	}
	_ = syscall.UtimesNano(path, []syscall.Timespec{{now, 0}, st.Mtim})
}

func writeAtomic(dest string, r io.Reader) (int64, error) {
	tf, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".*")
	if err != nil {
		return 0, err
	}
	size, err := io.Copy(tf, r)
	if err != nil {
		tf.Close()
		os.Remove(tf.Name())
		return 0, err
	}
	if err := tf.Chmod(0o644); err != nil {
		os.Remove(tf.Name())
		return 0, err
	}
	if err := tf.Close(); err != nil {
		os.Remove(tf.Name())
		return 0, err
	}
	if err := os.Rename(tf.Name(), dest); err != nil {
		os.Remove(tf.Name())
		return 0, err
	}
	return size, nil
}
