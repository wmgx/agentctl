package logclean

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

// Run deletes log files older than retentionDays inside logDir (recursive).
// Only files with .log extension are removed; directories are left intact.
func Run(logDir string, retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	removed := 0

	err := filepath.WalkDir(logDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(d.Name()) != ".log" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			if removeErr := os.Remove(path); removeErr != nil {
				log.Printf("[logclean] failed to remove %s: %v", path, removeErr)
			} else {
				log.Printf("[logclean] removed %s (age: %.0fd)", path, time.Since(info.ModTime()).Hours()/24)
				removed++
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("[logclean] walk error: %v", err)
		return
	}
	if removed > 0 {
		log.Printf("[logclean] cleaned %d log file(s) older than %d days", removed, retentionDays)
	}
}
