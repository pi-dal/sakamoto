package s3sync

import (
	"errors"
	"os"
	"sort"
)

// Preflight every destination, retain old bytes, and write baseline last. An
// ordinary I/O error rolls back applied local sources rather than acknowledging
// an incomplete pass. The next pass safely retries after an interrupted process.
func adoptFiles(writes map[string][]byte, baseline string) error {
	type prior struct {
		raw    []byte
		exists bool
	}
	previous := map[string]prior{}
	names := make([]string, 0, len(writes))
	for name := range writes {
		if err := safePath(name); err != nil {
			return err
		}
		info, err := os.Lstat(name)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && (!info.Mode().IsRegular() || info.Size() > 32<<20) {
			return errors.New("unsafe S3 adoption destination")
		}
		p := prior{exists: err == nil}
		if p.exists {
			p.raw, err = os.ReadFile(name)
			if err != nil {
				return err
			}
		}
		previous[name] = p
		if name != baseline {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	names = append(names, baseline)
	var applied []string
	for _, name := range names {
		if err := privateWrite(name, writes[name]); err != nil {
			for i := len(applied) - 1; i >= 0; i-- {
				n := applied[i]
				p := previous[n]
				if p.exists {
					_ = privateWrite(n, p.raw)
				} else {
					_ = os.Remove(n)
				}
			}
			return err
		}
		applied = append(applied, name)
	}
	return nil
}
