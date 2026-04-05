package ai

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// DiskInfo describes available storage at a path.
type DiskInfo struct {
	Path       string `json:"path"`
	TotalBytes uint64 `json:"total_bytes"`
	AvailBytes uint64 `json:"available_bytes"`
	TotalHuman string `json:"total_human"`
	AvailHuman string `json:"available_human"`
}

// MemInfo describes system physical memory.
type MemInfo struct {
	TotalBytes uint64 `json:"total_bytes"`
	TotalHuman string `json:"total_human"`
}

// GetDiskInfo returns disk usage for the filesystem containing path.
func GetDiskInfo(path string) (DiskInfo, error) {
	out, err := exec.Command("df", "-k", path).Output()
	if err != nil {
		return DiskInfo{}, fmt.Errorf("df: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return DiskInfo{}, fmt.Errorf("unexpected df output")
	}

	// Parse second line: filesystem 1K-blocks used available capacity mount
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return DiskInfo{}, fmt.Errorf("unexpected df fields: %d", len(fields))
	}

	totalKB, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return DiskInfo{}, fmt.Errorf("parse total: %w", err)
	}
	availKB, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		return DiskInfo{}, fmt.Errorf("parse available: %w", err)
	}

	total := totalKB * 1024
	avail := availKB * 1024

	return DiskInfo{
		Path:       path,
		TotalBytes: total,
		AvailBytes: avail,
		TotalHuman: HumanBytes(total),
		AvailHuman: HumanBytes(avail),
	}, nil
}

// GetMemInfo returns total physical memory.
func GetMemInfo() (MemInfo, error) {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return MemInfo{}, fmt.Errorf("sysctl hw.memsize: %w", err)
	}

	bytes, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return MemInfo{}, fmt.Errorf("parse memsize: %w", err)
	}

	return MemInfo{
		TotalBytes: bytes,
		TotalHuman: HumanBytes(bytes),
	}, nil
}

// HumanBytes formats a byte count as a human-readable string.
func HumanBytes(b uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
		TB = 1024 * GB
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.0f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.0f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
