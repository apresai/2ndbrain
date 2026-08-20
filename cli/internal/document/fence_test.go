package document

import "testing"

func TestFenceTracker_HeadingLevel(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []int
	}{
		{
			name:  "quadruple opener not closed by triple",
			lines: []string{"````", "```", "# x", "````", "# Real"},
			want:  []int{0, 0, 0, 0, 1},
		},
		{
			name:  "tilde not closed by backtick",
			lines: []string{"~~~", "```", "# x", "~~~", "# Real"},
			want:  []int{0, 0, 0, 0, 1},
		},
		{
			name:  "closer with info does not close",
			lines: []string{"```", "``` not-a-close", "# x", "```", "# Real"},
			want:  []int{0, 0, 0, 0, 1},
		},
		{
			name:  "info string on opener is ignored",
			lines: []string{"```bash", "# comment", "```", "# Real"},
			want:  []int{0, 0, 0, 1},
		},
		{
			name:  "unclosed fence skips later heading",
			lines: []string{"```", "# Heading"},
			want:  []int{0, 0},
		},
		{
			name:  "CRLF closer still closes",
			lines: []string{"```bash\r", "# comment\r", "```\r", "# Real\r"},
			want:  []int{0, 0, 0, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f fenceTracker
			for i, line := range tt.lines {
				if got := f.headingLevel(line); got != tt.want[i] {
					t.Fatalf("line %d %q: headingLevel = %d, want %d", i, line, got, tt.want[i])
				}
			}
		})
	}
}
