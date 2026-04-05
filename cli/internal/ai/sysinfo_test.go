package ai

import (
	"testing"
)

func TestGetDiskInfo(t *testing.T) {
	info, err := GetDiskInfo("/tmp")
	if err != nil {
		t.Fatalf("GetDiskInfo: %v", err)
	}
	if info.TotalBytes == 0 {
		t.Error("total bytes is 0")
	}
	if info.AvailBytes == 0 {
		t.Error("available bytes is 0")
	}
	if info.TotalBytes < info.AvailBytes {
		t.Errorf("total (%d) < available (%d)", info.TotalBytes, info.AvailBytes)
	}
	if info.TotalHuman == "" {
		t.Error("total human string empty")
	}
	if info.AvailHuman == "" {
		t.Error("available human string empty")
	}
	t.Logf("disk: %s total, %s available", info.TotalHuman, info.AvailHuman)
}

func TestGetMemInfo(t *testing.T) {
	info, err := GetMemInfo()
	if err != nil {
		t.Fatalf("GetMemInfo: %v", err)
	}
	// Should be at least 1 GB
	if info.TotalBytes < 1024*1024*1024 {
		t.Errorf("total bytes %d seems too low", info.TotalBytes)
	}
	// Should be less than 1 TB
	if info.TotalBytes > 1024*1024*1024*1024 {
		t.Errorf("total bytes %d seems too high", info.TotalBytes)
	}
	if info.TotalHuman == "" {
		t.Error("total human string empty")
	}
	t.Logf("memory: %s", info.TotalHuman)
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		input uint64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1 KB"},
		{1536 * 1024, "2 MB"},
		{278 * 1024 * 1024, "278 MB"},
		{16 * 1024 * 1024 * 1024, "16.0 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := HumanBytes(tt.input)
			if got != tt.want {
				t.Errorf("HumanBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
